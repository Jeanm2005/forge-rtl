package control

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Runner interface {
	Validate(context.Context, []byte) error
	Run(context.Context, *Job, *Attempt) error
	Stop(context.Context, string) error
	AttemptRoot(string, string) string
}
type Worker struct{ Binary, Root, Toolchain, Runs string }

func evalUnit(id string) string   { return "forge-eval-" + id + ".service" }
func launchUnit(id string) string { return "forge-launch-" + id + ".service" }
func (w *Worker) AttemptRoot(jobID, attemptID string) string {
	return filepath.Join(w.Runs, fmt.Sprintf("%d-%s-%s", len(jobID), jobID, attemptID))
}

type boundedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	max    int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	length := len(p)
	if b.buffer.Len() < b.max {
		b.buffer.Write(p[:min(len(p), b.max-b.buffer.Len())])
	}
	return length, nil
}
func (b *boundedBuffer) Value() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buffer.String() }
func (w *Worker) Validate(ctx context.Context, source []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, w.Binary, "validate")
	cmd.Stdin = bytes.NewReader(source)
	output := &boundedBuffer{max: 8192}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return fmt.Errorf("%w: %s", ErrCandidateInvalid, strings.TrimSpace(output.Value()))
		}
		return err
	}
	var result struct {
		Version uint32 `json:"schema_version"`
		Valid   bool   `json:"valid"`
	}
	if err := json.Unmarshal([]byte(output.Value()), &result); err != nil || result.Version != 1 || !result.Valid {
		return errors.New("worker validation contract mismatch")
	}
	return nil
}
func (w *Worker) Run(ctx context.Context, j *Job, a *Attempt) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := os.MkdirAll(w.Runs, 0700); err != nil {
		return err
	}
	submission := filepath.Join(w.Runs, "sources", a.ID+".sv")
	if err := atomicWrite(submission, j.Source); err != nil {
		return err
	}
	args := []string{"--user", "--quiet", "--wait", "--pipe", "--service-type=exec", "--unit", launchUnit(a.ID), "--property=KillMode=control-group", "--property=TimeoutStopSec=15", "--property=MemoryMax=536870912", "--property=TasksMax=32", fmt.Sprintf("--property=RuntimeMaxSec=%d", max(1, (j.Deadline-time.Now().UnixMilli())/1000)+30), "--setenv=FORGE_CONTROLLED_UNIT=" + evalUnit(a.ID)}
	for _, name := range []string{"XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"} {
		if value, ok := os.LookupEnv(name); ok {
			args = append(args, "--setenv="+name+"="+value)
		}
	}
	args = append(args, "--", w.Binary, "evaluate", w.Root, w.Runs, j.ID, a.ID, submission, w.Toolchain)
	cmd := exec.Command("/usr/bin/systemd-run", args...)
	output := &boundedBuffer{max: 8192}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("worker process: %w: %s", err, output.Value())
		}
		return nil
	case <-ctx.Done():
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		stopErr := w.Stop(cleanupCtx, a.ID)
		select {
		case <-done:
		case <-cleanupCtx.Done():
			_ = cmd.Process.Kill()
			<-done
		}
		if stopErr != nil {
			return stopErr
		}
		return ctx.Err()
	}
}
func (w *Worker) Stop(ctx context.Context, id string) error {
	if !validID(id, "a") {
		return errors.New("invalid cleanup identity")
	}
	for _, unit := range []string{launchUnit(id), evalUnit(id)} {
		command := exec.CommandContext(ctx, "/usr/bin/systemctl", "--user", "stop", unit)
		output := &boundedBuffer{max: 8192}
		command.Stdout = output
		command.Stderr = output
		if err := command.Run(); err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 5 {
				return fmt.Errorf("stop %s: %w: %s", unit, err, output.Value())
			}
		}
		reset := exec.CommandContext(ctx, "/usr/bin/systemctl", "--user", "reset-failed", unit)
		_ = reset.Run()
	}
	return nil
}
