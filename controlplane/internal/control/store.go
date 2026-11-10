package control

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	pb "github.com/Jeanm2005/forge-rtl/controlplane/api/v1"
)

var (
	ErrNotFound         = errors.New("job or attempt not found")
	ErrConflict         = errors.New("idempotency key already belongs to a different request")
	ErrStale            = errors.New("attempt is no longer active or its lease has expired")
	ErrIneligible       = errors.New("job lacks passing verified evidence")
	ErrCandidateInvalid = errors.New("invalid candidate")
)

const CandidatePath = "rtl/candidate/mac_candidate.sv"
const MaxSource = 1024 * 1024

type Attempt struct {
	ID          string          `json:"id"`
	State       pb.AttemptState `json:"state"`
	LeaseUntil  int64           `json:"lease_until"`
	Outcome     string          `json:"outcome"`
	Diagnostic  string          `json:"diagnostic,omitempty"`
	Manifest    string          `json:"manifest,omitempty"`
	ManifestSHA string          `json:"manifest_sha256,omitempty"`
}
type Job struct {
	ID          string      `json:"id"`
	Key         string      `json:"key"`
	Fingerprint string      `json:"fingerprint"`
	Source      []byte      `json:"source"`
	SourceSHA   string      `json:"source_sha256"`
	State       pb.JobState `json:"state"`
	Created     int64       `json:"created"`
	Deadline    int64       `json:"deadline"`
	MaxAttempts uint32      `json:"max_attempts"`
	Eligible    bool        `json:"eligible"`
	Outcome     string      `json:"outcome"`
	Diagnostic  string      `json:"diagnostic,omitempty"`
	Attempts    []*Attempt  `json:"attempts"`
}
type snapshot struct {
	Version   uint32          `json:"version"`
	PolicySHA string          `json:"policy_sha256"`
	Jobs      map[string]*Job `json:"jobs"`
}
type Store struct {
	mu       sync.Mutex
	root     string
	lock     *os.File
	data     snapshot
	now      func() time.Time
	poisoned error
}

func OpenStore(root string, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(root, "LOCK"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("state directory already owned: %w", err)
	}
	s := &Store{root: root, lock: lock, now: now, data: snapshot{Version: 1, Jobs: map[string]*Job{}}}
	bytes, err := regularFile(root, "state.json", 100*1024*1024)
	if err == nil {
		err = json.Unmarshal(bytes, &s.data)
		if err == nil && (s.data.Version != 1 || s.data.Jobs == nil) {
			err = errors.New("unsupported or incomplete state snapshot")
		}
		if err == nil {
			err = s.validateSnapshot()
		}
	} else if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) validateSnapshot() error {
	if len(s.data.Jobs) > 64 {
		return errors.New("stored job quota exceeded")
	}
	keys := map[string]bool{}
	attempts := map[string]bool{}
	for id, j := range s.data.Jobs {
		if j == nil || id != j.ID || !validID(id, "j") || j.Key == "" || keys[j.Key] || digest(j.Source) != j.SourceSHA || len(j.Source) > MaxSource || j.MaxAttempts < 1 || j.MaxAttempts > 3 || j.Deadline <= j.Created {
			return errors.New("invalid stored job")
		}
		keys[j.Key] = true
		if _, ok := pb.JobState_name[int32(j.State)]; !ok || j.State == pb.JobState_JOB_STATE_UNSPECIFIED {
			return errors.New("invalid stored job state")
		}
		active := 0
		for _, a := range j.Attempts {
			if a == nil || !validID(a.ID, "a") || attempts[a.ID] {
				return errors.New("invalid stored attempt")
			}
			attempts[a.ID] = true
			if _, ok := pb.AttemptState_name[int32(a.State)]; !ok || a.State == pb.AttemptState_ATTEMPT_STATE_UNSPECIFIED {
				return errors.New("invalid stored attempt state")
			}
			if a.State == pb.AttemptState_LEASED {
				active++
			}
			if a.Manifest != "" && a.Manifest != filepath.Join("evidence", a.ID, "manifest.json") {
				return errors.New("invalid stored evidence path")
			}
		}
		if active > 1 || (j.State == pb.JobState_RUNNING) != (active == 1) || len(j.Attempts) > int(j.MaxAttempts) {
			return errors.New("inconsistent stored attempt state")
		}
		if j.State == pb.JobState_QUEUED && len(j.Attempts) >= int(j.MaxAttempts) {
			return errors.New("queued job exhausted attempt budget")
		}
		if j.Eligible && (j.State != pb.JobState_COMPLETED && j.State != pb.JobState_AWAITING_REVIEW || j.Outcome != "passed" || len(j.Attempts) == 0 || j.Attempts[len(j.Attempts)-1].ManifestSHA == "") {
			return errors.New("stored eligibility lacks evidence")
		}
	}
	return nil
}
func (s *Store) Close() error   { return s.lock.Close() }
func digest(data []byte) string { hash := sha256.Sum256(data); return hex.EncodeToString(hash[:]) }
func newID(prefix string) string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(bytes[:])
}
func validID(id, prefix string) bool {
	if len(id) != 33 || id[:1] != prefix {
		return false
	}
	_, err := hex.DecodeString(id[1:])
	return err == nil
}
func clone[T any](value T) T {
	b, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var copy T
	if err = json.Unmarshal(b, &copy); err != nil {
		panic(err)
	}
	return copy
}
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".publish-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (s *Store) change(fn func(*snapshot) error) error {
	if s.poisoned != nil {
		return s.poisoned
	}
	next := clone(s.data)
	if err := fn(&next); err != nil {
		return err
	}
	bytes, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err = atomicWrite(filepath.Join(s.root, "state.json"), bytes); err != nil {
		s.poisoned = fmt.Errorf("state publication failed; restart required: %w", err)
		return s.poisoned
	}
	s.data = next
	return nil
}
func submissionFingerprint(req *pb.SubmitRequest) string {
	data, _ := json.Marshal(struct {
		Path              string
		Source            []byte
		Timeout, Attempts uint32
	}{req.CandidatePath, req.CandidateSource, req.TimeoutSeconds, req.MaxAttempts})
	return digest(data)
}
func (s *Store) Replay(req *pb.SubmitRequest) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.poisoned != nil {
		return nil, s.poisoned
	}
	fingerprint := submissionFingerprint(req)
	for _, j := range s.data.Jobs {
		if j.Key == req.IdempotencyKey {
			if j.Fingerprint != fingerprint {
				return nil, ErrConflict
			}
			return clone(j), nil
		}
	}
	return nil, nil
}
func (s *Store) Submit(req *pb.SubmitRequest) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fingerprint := submissionFingerprint(req)
	for _, job := range s.data.Jobs {
		if job.Key == req.IdempotencyKey {
			if job.Fingerprint != fingerprint {
				return nil, ErrConflict
			}
			return clone(job), nil
		}
	}
	if len(s.data.Jobs) >= 64 {
		return nil, errors.New("job retention quota reached (64); use a new state directory")
	}
	now := s.now().UnixMilli()
	job := &Job{ID: newID("j"), Key: req.IdempotencyKey, Fingerprint: fingerprint, Source: append([]byte(nil), req.CandidateSource...), SourceSHA: digest(req.CandidateSource), State: pb.JobState_QUEUED, Created: now, Deadline: now + int64(req.TimeoutSeconds)*1000, MaxAttempts: req.MaxAttempts, Attempts: []*Attempt{}}
	err := s.change(func(next *snapshot) error { next.Jobs[job.ID] = job; return nil })
	return clone(job), err
}
func (s *Store) Get(id string) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.poisoned != nil {
		return nil, s.poisoned
	}
	job, ok := s.data.Jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return clone(job), nil
}
func active(j *Job, aID string, now int64) (*Attempt, error) {
	if j == nil || j.State != pb.JobState_RUNNING || len(j.Attempts) == 0 {
		return nil, ErrStale
	}
	a := j.Attempts[len(j.Attempts)-1]
	if a.ID != aID || a.State != pb.AttemptState_LEASED || a.LeaseUntil <= now || j.Deadline <= now {
		return nil, ErrStale
	}
	return a, nil
}
func (s *Store) Claim(lease time.Duration) (*Job, *Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	var queue []*Job
	for _, job := range s.data.Jobs {
		if job.State == pb.JobState_QUEUED && job.Deadline > now {
			queue = append(queue, job)
		}
	}
	sort.Slice(queue, func(i, j int) bool {
		if queue[i].Created == queue[j].Created {
			return queue[i].ID < queue[j].ID
		}
		return queue[i].Created < queue[j].Created
	})
	if len(queue) == 0 {
		return nil, nil, nil
	}
	id := queue[0].ID
	a := &Attempt{ID: newID("a"), State: pb.AttemptState_LEASED, LeaseUntil: min(now+lease.Milliseconds(), queue[0].Deadline)}
	err := s.change(func(next *snapshot) error {
		job := next.Jobs[id]
		job.State = pb.JobState_RUNNING
		job.Attempts = append(job.Attempts, a)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return clone(s.data.Jobs[id]), clone(a), nil
}
func (s *Store) Heartbeat(jobID, attemptID string, lease time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	return s.change(func(next *snapshot) error {
		j := next.Jobs[jobID]
		a, err := active(j, attemptID, now)
		if err != nil {
			return err
		}
		a.LeaseUntil = min(now+lease.Milliseconds(), j.Deadline)
		return nil
	})
}
func retry(j *Job, now int64) {
	j.Eligible = false
	if len(j.Attempts) < int(j.MaxAttempts) && now < j.Deadline {
		j.State = pb.JobState_QUEUED
		j.Outcome = ""
	} else {
		j.State = pb.JobState_COMPLETED
		j.Outcome = "infrastructure_error"
	}
}
func (s *Store) Sweep(recovering bool) ([]Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	var stops []Attempt
	needs := false
	for _, j := range s.data.Jobs {
		if j.State == pb.JobState_RUNNING && (recovering || j.Deadline <= now || j.Attempts[len(j.Attempts)-1].LeaseUntil <= now) || j.State == pb.JobState_QUEUED && j.Deadline <= now {
			needs = true
		}
	}
	if !needs {
		return nil, nil
	}
	err := s.change(func(next *snapshot) error {
		for _, j := range next.Jobs {
			if j.State == pb.JobState_QUEUED && j.Deadline <= now {
				j.State = pb.JobState_TIMED_OUT
				j.Outcome = "timeout"
			}
			if j.State != pb.JobState_RUNNING {
				continue
			}
			a := j.Attempts[len(j.Attempts)-1]
			if !recovering && j.Deadline > now && a.LeaseUntil > now {
				continue
			}
			stops = append(stops, *a)
			a.Outcome = "infrastructure_error"
			a.Diagnostic = "lease expired"
			a.State = pb.AttemptState_EXPIRED
			if recovering {
				a.State = pb.AttemptState_ABANDONED
				a.Diagnostic = "control plane restarted"
			}
			retry(j, now)
			if j.Deadline <= now {
				a.State = pb.AttemptState_ATTEMPT_TIMED_OUT
				a.Outcome = "timeout"
				j.State = pb.JobState_TIMED_OUT
				j.Outcome = "timeout"
			}
		}
		return nil
	})
	return stops, err
}
func (s *Store) Cancel(id string) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.data.Jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	if j.State != pb.JobState_QUEUED && j.State != pb.JobState_RUNNING {
		return clone(j), nil
	}
	err := s.change(func(next *snapshot) error {
		job := next.Jobs[id]
		job.State = pb.JobState_CANCELLED
		job.Eligible = false
		job.Outcome = "cancelled"
		if len(job.Attempts) > 0 {
			a := job.Attempts[len(job.Attempts)-1]
			if a.State == pb.AttemptState_LEASED {
				a.State = pb.AttemptState_ATTEMPT_CANCELLED
				a.Outcome = "cancelled"
			}
		}
		return nil
	})
	return clone(s.data.Jobs[id]), err
}

type completion struct {
	Outcome, Diagnostic, Manifest, ManifestSHA string
	Eligible, Retryable                        bool
}

func (s *Store) Finish(jobID, attemptID string, result completion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	return s.change(func(next *snapshot) error {
		j := next.Jobs[jobID]
		a, err := active(j, attemptID, now)
		if err != nil {
			return err
		}
		a.State = pb.AttemptState_FINISHED
		a.Outcome = result.Outcome
		a.Diagnostic = result.Diagnostic
		a.Manifest = result.Manifest
		a.ManifestSHA = result.ManifestSHA
		j.State = pb.JobState_COMPLETED
		j.Outcome = result.Outcome
		j.Diagnostic = result.Diagnostic
		j.Eligible = result.Eligible && result.Outcome == "passed" && result.ManifestSHA != ""
		if result.Retryable {
			retry(j, now)
		}
		return nil
	})
}
func (s *Store) QueueReview(id, manifestSHA string) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.data.Jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	if !job.Eligible || job.State != pb.JobState_COMPLETED && job.State != pb.JobState_AWAITING_REVIEW || len(job.Attempts) == 0 || job.Attempts[len(job.Attempts)-1].ManifestSHA != manifestSHA {
		return nil, ErrIneligible
	}
	if job.State == pb.JobState_AWAITING_REVIEW {
		return clone(job), nil
	}
	err := s.change(func(next *snapshot) error { next.Jobs[id].State = pb.JobState_AWAITING_REVIEW; return nil })
	return clone(s.data.Jobs[id]), err
}

func (s *Store) AllAttempts() []Attempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	var attempts []Attempt
	for _, j := range s.data.Jobs {
		for _, a := range j.Attempts {
			attempts = append(attempts, *a)
		}
	}
	return attempts
}
func (s *Store) BindPolicy(hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.PolicySHA != "" && s.data.PolicySHA != hash {
		return errors.New("trusted evaluation policy changed; use a new state directory")
	}
	if s.data.PolicySHA == hash {
		return nil
	}
	return s.change(func(next *snapshot) error { next.PolicySHA = hash; return nil })
}
