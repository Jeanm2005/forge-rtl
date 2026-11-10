package control

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	pb "github.com/Jeanm2005/forge-rtl/controlplane/api/v1"
)

type Engine struct {
	Store     *Store
	Runner    Runner
	Validator *Validator
	Lease     time.Duration
	Tick      time.Duration
	mu        sync.Mutex
	current   string
	cancel    context.CancelFunc
	wake      chan struct{}
}

func NewEngine(store *Store, runner Runner, validator *Validator) *Engine {
	return &Engine{Store: store, Runner: runner, Validator: validator, Lease: 15 * time.Second, Tick: 100 * time.Millisecond, wake: make(chan struct{}, 1)}
}
func (e *Engine) Notify() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}
func (e *Engine) CancelAttempt(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.current == id && e.cancel != nil {
		e.cancel()
	}
	e.Notify()
}
func (e *Engine) Recover(ctx context.Context) error {
	if err := e.Store.BindPolicy(e.Validator.PolicySHA); err != nil {
		return err
	}
	attempts := e.Store.AllAttempts()
	if _, err := e.Store.Sweep(true); err != nil {
		return err
	}
	for _, a := range attempts {
		if err := e.Runner.Stop(ctx, a.ID); err != nil {
			return fmt.Errorf("restart cleanup: %w", err)
		}
	}
	return nil
}
func (e *Engine) Run(ctx context.Context) error {
	ticker := time.NewTicker(e.Tick)
	defer ticker.Stop()
	done := make(chan error, 1)
	running := false
	defer func() {
		e.mu.Lock()
		if e.cancel != nil {
			e.cancel()
		}
		e.mu.Unlock()
		if running {
			<-done
		}
	}()
	for {
		if ctx.Err() != nil {
			return nil
		}
		stops, err := e.Store.Sweep(false)
		if err != nil {
			return err
		}
		for _, a := range stops {
			e.CancelAttempt(a.ID)
		}
		if !running {
			job, attempt, err := e.Store.Claim(e.Lease)
			if err != nil {
				return err
			}
			if job != nil {
				attemptCtx, cancel := context.WithDeadline(ctx, time.UnixMilli(job.Deadline))
				e.mu.Lock()
				e.current = attempt.ID
				e.cancel = cancel
				e.mu.Unlock()
				running = true
				go func() { err := e.runAttempt(attemptCtx, job, attempt); cancel(); done <- err }()
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case err := <-done:
			running = false
			e.mu.Lock()
			e.current = ""
			e.cancel = nil
			e.mu.Unlock()
			if err != nil {
				return err
			}
		case <-e.wake:
		case <-ticker.C:
		}
	}
}
func (e *Engine) runAttempt(ctx context.Context, job *Job, attempt *Attempt) (resultErr error) {
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := e.Runner.Stop(cleanupCtx, attempt.ID); err != nil {
			resultErr = fmt.Errorf("attempt cleanup failed: %w", err)
		}
	}()
	initial, err := e.Store.Get(job.ID)
	if err != nil {
		return err
	}
	if _, err = active(initial, attempt.ID, e.Store.now().UnixMilli()); err != nil {
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}
	heartbeatCtx, stop := context.WithCancel(ctx)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(e.Lease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if err := e.Store.Heartbeat(job.ID, attempt.ID, e.Lease); err != nil {
					e.CancelAttempt(attempt.ID)
					return
				}
			}
		}
	}()
	defer func() { stop(); <-heartbeatDone }()
	runErr := e.Runner.Run(ctx, job, attempt)
	if ctx.Err() != nil {
		return nil
	}
	current, err := e.Store.Get(job.ID)
	if err != nil {
		return nil
	}
	if _, err = active(current, attempt.ID, e.Store.now().UnixMilli()); err != nil {
		return nil
	}
	result, raw, err := e.Validator.Check(job, attempt, e.Runner.AttemptRoot(job.ID, attempt.ID))
	if err != nil {
		result = completion{Outcome: "infrastructure_error", Diagnostic: err.Error(), Retryable: runErr != nil}
		if runErr != nil {
			result.Diagnostic = runErr.Error() + "; " + result.Diagnostic
		}
	}
	if err == nil {
		if runErr != nil {
			result.Eligible = false
			result.Outcome = "infrastructure_error"
			result.Diagnostic = runErr.Error()
			result.Retryable = true
		}
		if err = syncDir(e.Runner.AttemptRoot(job.ID, attempt.ID)); err == nil {
			err = syncDir(filepath.Dir(e.Runner.AttemptRoot(job.ID, attempt.ID)))
		}
		if err != nil {
			_ = e.Store.Finish(job.ID, attempt.ID, completion{Outcome: "infrastructure_error", Diagnostic: err.Error()})
			return nil
		}
		result.Manifest, result.ManifestSHA, err = e.Store.PublishEvidence(attempt.ID, raw)
		if err != nil {
			result = completion{Outcome: "infrastructure_error", Diagnostic: "evidence publication: " + err.Error()}
		}
	}
	if err = e.Store.Finish(job.ID, attempt.ID, result); err != nil && !errors.Is(err, ErrStale) {
		return err
	}
	return nil
}
func (e *Engine) Evidence(jobID, attemptID string) (*pb.Evidence, error) {
	j, err := e.Store.Get(jobID)
	if err != nil {
		return nil, err
	}
	var a *Attempt
	for _, attempt := range j.Attempts {
		if attemptID == "" || attempt.ID == attemptID {
			a = attempt
		}
	}
	if a == nil || a.ManifestSHA == "" {
		return nil, ErrNotFound
	}
	raw, err := regularFile(e.Store.root, a.Manifest, 8*1024*1024)
	if err != nil || digest(raw) != a.ManifestSHA {
		return nil, ErrIneligible
	}
	return &pb.Evidence{JobId: j.ID, AttemptId: a.ID, ManifestJson: raw, ManifestSha256: a.ManifestSHA, ArtifactDirectory: e.Runner.AttemptRoot(j.ID, a.ID) + "/artifacts"}, nil
}
