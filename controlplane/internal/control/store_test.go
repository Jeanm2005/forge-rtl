package control

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/Jeanm2005/forge-rtl/controlplane/api/v1"
)

func request(key string) *pb.SubmitRequest {
	return &pb.SubmitRequest{IdempotencyKey: key, CandidatePath: CandidatePath, CandidateSource: []byte("module mac_candidate; endmodule\n"), TimeoutSeconds: 120, MaxAttempts: 2}
}
func clockStore(t *testing.T) (*Store, *atomic.Int64) {
	t.Helper()
	now := &atomic.Int64{}
	now.Store(time.Now().UnixMilli())
	s, err := OpenStore(t.TempDir(), func() time.Time { return time.UnixMilli(now.Load()) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, now
}
func mustSubmit(t *testing.T, s *Store, key string) *Job {
	t.Helper()
	j, err := s.Submit(request(key))
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func claim(t *testing.T, s *Store) (*Job, *Attempt) {
	t.Helper()
	j, a, err := s.Claim(15 * time.Second)
	if err != nil || j == nil {
		t.Fatalf("claim: %v", err)
	}
	return j, a
}

func TestConcurrentIdempotencyAndConflictingPayload(t *testing.T) {
	s, _ := clockStore(t)
	var wg sync.WaitGroup
	ids := make(chan string, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, err := s.Submit(request("same"))
			if err != nil {
				t.Error(err)
				return
			}
			ids <- job.ID
		}()
	}
	wg.Wait()
	close(ids)
	var id string
	for value := range ids {
		if id != "" && id != value {
			t.Fatal("idempotent submit created multiple jobs")
		}
		id = value
	}
	req := request("same")
	req.CandidateSource = []byte("changed")
	if _, err := s.Submit(req); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	j, a := claim(t, s)
	if j.ID != id || len(j.Attempts) != 1 || a.ID == j.ID {
		t.Fatal("job/attempt identity failure")
	}
	j.Source[0] = 'X'
	saved, _ := s.Get(id)
	if saved.Source[0] == 'X' {
		t.Fatal("caller mutated durable state")
	}
}
func TestLeaseExpiryFencesHeartbeatAndCompletion(t *testing.T) {
	s, now := clockStore(t)
	mustSubmit(t, s, "lease")
	j, a := claim(t, s)
	now.Add(15000)
	if err := s.Heartbeat(j.ID, a.ID, 15*time.Second); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	if err := s.Finish(j.ID, a.ID, completion{Outcome: "passed", Eligible: true, ManifestSHA: "fake"}); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	stops, err := s.Sweep(false)
	if err != nil || len(stops) != 1 {
		t.Fatalf("sweep: %v %v", stops, err)
	}
	j2, a2 := claim(t, s)
	if j2.ID != j.ID || a2.ID == a.ID {
		t.Fatal("retry must keep job and replace attempt")
	}
	if err := s.Finish(j.ID, a.ID, completion{Outcome: "passed", Eligible: true}); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	now.Add(15000)
	if _, err = s.Sweep(false); err != nil {
		t.Fatal(err)
	}
	final, _ := s.Get(j.ID)
	if final.State != pb.JobState_COMPLETED || final.Outcome != "infrastructure_error" || final.Eligible || len(final.Attempts) != 2 {
		t.Fatalf("unbounded retry: %+v", final)
	}
}
func TestHeartbeatCannotExtendJobDeadline(t *testing.T) {
	s, now := clockStore(t)
	req := request("deadline")
	req.TimeoutSeconds = 2
	j, err := s.Submit(req)
	if err != nil {
		t.Fatal(err)
	}
	_, a := claim(t, s)
	now.Add(1000)
	if err = s.Heartbeat(j.ID, a.ID, time.Minute); err != nil {
		t.Fatal(err)
	}
	current, _ := s.Get(j.ID)
	if current.Attempts[0].LeaseUntil != j.Deadline {
		t.Fatal("lease escaped deadline")
	}
	now.Add(1000)
	if _, err = s.Sweep(false); err != nil {
		t.Fatal(err)
	}
	current, _ = s.Get(j.ID)
	if current.State != pb.JobState_TIMED_OUT || current.Eligible {
		t.Fatal(current)
	}
	if err = s.Finish(j.ID, a.ID, completion{Outcome: "passed", Eligible: true}); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
}
func TestCancellationFencesAndSurvivesRestart(t *testing.T) {
	s, _ := clockStore(t)
	j := mustSubmit(t, s, "cancel")
	_, a := claim(t, s)
	if _, err := s.Cancel(j.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(j.ID, a.ID, completion{Outcome: "passed", Eligible: true}); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(s.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	current, _ := reopened.Get(j.ID)
	if current.State != pb.JobState_CANCELLED || current.Eligible {
		t.Fatal(current)
	}
}
func TestRestartRecoveryAndExclusiveStateOwnership(t *testing.T) {
	s, _ := clockStore(t)
	j := mustSubmit(t, s, "restart")
	_, a := claim(t, s)
	if other, err := OpenStore(s.root, nil); err == nil {
		other.Close()
		t.Fatal("multiple schedulers own state")
	}
	s.Close()
	reopened, err := OpenStore(s.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stops, err := reopened.Sweep(true)
	if err != nil || len(stops) != 1 {
		t.Fatal(err)
	}
	recovered, _ := reopened.Get(j.ID)
	if recovered.State != pb.JobState_QUEUED || recovered.Attempts[0].State != pb.AttemptState_ABANDONED {
		t.Fatal(recovered)
	}
	if err = reopened.Finish(j.ID, a.ID, completion{Outcome: "passed", Eligible: true}); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	replay, err := reopened.Submit(request("restart"))
	if err != nil || replay.ID != j.ID {
		t.Fatal("idempotency lost on restart", err)
	}
	_, next := claim(t, reopened)
	if next.ID == a.ID {
		t.Fatal("attempt reused on restart")
	}
}
func TestRetriesOnlyWithinBudgetAndVerificationNeverRetries(t *testing.T) {
	for _, test := range []struct {
		name    string
		retry   bool
		outcome string
	}{{"infra", true, "infrastructure_error"}, {"proof", false, "counterexample"}} {
		t.Run(test.name, func(t *testing.T) {
			s, _ := clockStore(t)
			mustSubmit(t, s, test.name)
			j, a := claim(t, s)
			if err := s.Finish(j.ID, a.ID, completion{Outcome: test.outcome, Retryable: test.retry}); err != nil {
				t.Fatal(err)
			}
			current, _ := s.Get(j.ID)
			if test.retry {
				if current.State != pb.JobState_QUEUED {
					t.Fatal(current)
				}
				_, a = claim(t, s)
				if err := s.Finish(j.ID, a.ID, completion{Outcome: test.outcome, Retryable: true}); err != nil {
					t.Fatal(err)
				}
			}
			current, _ = s.Get(j.ID)
			if current.State != pb.JobState_COMPLETED || current.Eligible {
				t.Fatal(current)
			}
		})
	}
}
func TestPersistenceFailurePoisonsMutationsAndPolicyChangesRejected(t *testing.T) {
	s, _ := clockStore(t)
	if err := s.BindPolicy("one"); err != nil {
		t.Fatal(err)
	}
	if err := s.BindPolicy("two"); err == nil {
		t.Fatal("policy silently changed")
	}
	path := filepath.Join(s.root, "state.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(request("failure")); err == nil {
		t.Fatal("acknowledged unpersisted submission")
	}
	if len(s.data.Jobs) != 0 {
		t.Fatal("failed transaction leaked into state")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(request("retry")); err == nil {
		t.Fatal("continued after ambiguous persistence failure")
	}
}
func TestMissingEvidenceCannotEnterReview(t *testing.T) {
	s, _ := clockStore(t)
	j := mustSubmit(t, s, "missing")
	_, a := claim(t, s)
	if err := s.Finish(j.ID, a.ID, completion{Outcome: "passed", Eligible: true}); err != nil {
		t.Fatal(err)
	}
	current, _ := s.Get(j.ID)
	if current.Eligible {
		t.Fatal("missing evidence granted eligibility")
	}
	if _, err := s.QueueReview(j.ID, ""); !errors.Is(err, ErrIneligible) {
		t.Fatal(err)
	}
}
