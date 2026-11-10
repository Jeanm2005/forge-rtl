package control

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	pb "github.com/Jeanm2005/forge-rtl/controlplane/api/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type fakeRunner struct {
	root      string
	validator *Validator
	run       func(context.Context, *Job, *Attempt) error
	mu        sync.Mutex
	stops     []string
	stopErr   error
}

func (f *fakeRunner) Validate(_ context.Context, source []byte) error {
	if string(source) == "reject" {
		return ErrCandidateInvalid
	}
	return nil
}
func (f *fakeRunner) Run(ctx context.Context, j *Job, a *Attempt) error {
	if f.run != nil {
		return f.run(ctx, j, a)
	}
	return f.emit(j, a, "passed", "")
}
func (f *fakeRunner) Stop(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, id)
	return f.stopErr
}
func (f *fakeRunner) AttemptRoot(j, a string) string { return filepath.Join(f.root, j, a) }
func fakeValidator() *Validator {
	v := &Validator{Inputs: map[string]FileEvidence{"gold.sv": evidenceOf([]byte("gold"))}, Worker: evidenceOf([]byte("trusted worker")), Toolchain: map[string]string{"VERSION": "pinned"}, Limits: DefaultLimits(), PolicySHA: "test-policy"}
	limits, _ := json.Marshal(v.Limits)
	v.ConfigurationSHA = digest(limits)
	return v
}
func (f *fakeRunner) emit(j *Job, a *Attempt, outcome, omit string) error {
	root := f.AttemptRoot(j.ID, a.ID)
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	zero := uint32(0)
	duration := uint64(1)
	exit := 0
	depth := uint32(3)
	versions := "Verilator\nYosys\nEQY\nSBY\nBitwuzla\ng++\nMake\nNode"
	m := Manifest{Version: 1, JobID: j.ID, AttemptID: a.ID, Recorded: 1, Outcome: outcome, Limits: f.validator.Limits, ConfigurationSHA: f.validator.ConfigurationSHA, Worker: f.validator.Worker, Inputs: map[string]FileEvidence{}, Artifacts: map[string]FileEvidence{}, Transport: map[string]FileEvidence{}, Toolchain: f.validator.Toolchain, Versions: &versions, Seed: "1badf00d", Strategy: "EQY SBY smtbmc bitwuzla induction depth 10; public matches only; first-transition reset", Gates: map[string]Gate{}, Cells: map[string]uint64{"gold": 988, "candidate": 988}, Execution: &Execution{Outcome: "completed", Exit: &exit, Unit: evalUnit(a.ID), ServiceResult: "success"}}
	write := func(category, relative string, contents []byte) error {
		path := filepath.Join(root, category, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(path, contents, 0600); err != nil {
			return err
		}
		return nil
	}
	for relative, expected := range f.validator.Inputs {
		if err := write("inputs", relative, []byte("gold")); err != nil {
			return err
		}
		m.Inputs[relative] = expected
	}
	if err := write("inputs", CandidatePath, j.Source); err != nil {
		return err
	}
	m.Inputs[CandidatePath] = evidenceOf(j.Source)
	for _, relative := range []string{"sim/result.log", "formal/PASS", "formal/matched.ids", "synth/comparison.json", "synth/gold/netlist.v", "synth/gold/netlist.json", "synth/gold/stat.json", "synth/candidate/netlist.v", "synth/candidate/netlist.json", "synth/candidate/stat.json"} {
		if relative == omit {
			continue
		}
		if err := write("artifacts", relative, []byte("fixture")); err != nil {
			return err
		}
		m.Artifacts[relative] = evidenceOf([]byte("fixture"))
	}
	for _, relative := range []string{"artifacts.tar", "execution.log"} {
		if err := write("", relative, []byte("transport")); err != nil {
			return err
		}
		m.Transport[relative] = evidenceOf([]byte("transport"))
	}
	for _, name := range []string{"tools", "simulation", "equivalence", "equivalence_audit", "reachable_depth_3", "synthesis"} {
		m.Gates[name] = Gate{Outcome: "passed", Exit: &zero, Duration: &duration}
	}
	bounded := m.Gates["reachable_depth_3"]
	bounded.Depth = &depth
	m.Gates["reachable_depth_3"] = bounded
	bytes, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "manifest.json"), bytes, 0600)
}
func testRPC(t *testing.T, e *Engine) pb.ControlClient {
	t.Helper()
	listener := bufconn.Listen(2 * 1024 * 1024)
	server := grpc.NewServer(grpc.MaxRecvMsgSize(2*1024*1024), grpc.MaxSendMsgSize(10*1024*1024))
	pb.RegisterControlServer(server, &Server{Engine: e})
	go server.Serve(listener)
	connection, err := grpc.NewClient("passthrough:///test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	if err = e.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	go func() { done <- e.Run(ctx) }()
	t.Cleanup(func() {
		connection.Close()
		server.Stop()
		listener.Close()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("engine failed to stop")
		}
	})
	return pb.NewControlClient(connection)
}
func fakeEngine(t *testing.T) (*Engine, *fakeRunner) {
	t.Helper()
	s, err := OpenStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	v := fakeValidator()
	r := &fakeRunner{root: filepath.Join(s.root, "runs"), validator: v}
	e := NewEngine(s, r, v)
	e.Tick = time.Millisecond * 5
	return e, r
}
func terminal(t *testing.T, client pb.ControlClient, id string) *pb.Job {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		j, err := client.Get(ctx, &pb.GetRequest{JobId: id})
		if err != nil {
			t.Fatal(err)
		}
		if j.State != pb.JobState_RUNNING && j.State != pb.JobState_QUEUED {
			return j
		}
		select {
		case <-ctx.Done():
			t.Fatal("job did not terminate")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestGRPCIdempotencyEvidenceAndHumanReview(t *testing.T) {
	e, r := fakeEngine(t)
	client := testRPC(t, e)
	ctx := context.Background()
	req := request("grpc")
	j, err := client.Submit(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := client.Submit(ctx, req)
	if err != nil || replay.JobId != j.JobId {
		t.Fatal("RPC idempotency lost", err)
	}
	conflict := request("grpc")
	conflict.CandidateSource = []byte("different")
	if _, err = client.Submit(ctx, conflict); status.Code(err) != codes.AlreadyExists {
		t.Fatal(err)
	}
	result := terminal(t, client, j.JobId)
	if !result.Eligible || len(result.Attempts) != 1 {
		t.Fatal(result)
	}
	evidence, err := client.GetEvidence(ctx, &pb.EvidenceRequest{JobId: j.JobId})
	if err != nil || digest(evidence.ManifestJson) != evidence.ManifestSha256 {
		t.Fatal("RPC evidence mismatch", err)
	}
	reviewed, err := client.QueueReview(ctx, &pb.GetRequest{JobId: j.JobId})
	if err != nil || reviewed.State != pb.JobState_AWAITING_REVIEW {
		t.Fatal(reviewed, err)
	}
	if _, err = client.QueueReview(ctx, &pb.GetRequest{JobId: j.JobId}); err != nil {
		t.Fatal("review replay", err)
	}
	path := filepath.Join(r.AttemptRoot(j.JobId, result.Attempts[0].AttemptId), "artifacts/formal/PASS")
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	result, err = client.Get(ctx, &pb.GetRequest{JobId: j.JobId})
	if err != nil || result.Eligible {
		t.Fatal("missing evidence still eligible", err)
	}
	if _, err = client.QueueReview(ctx, &pb.GetRequest{JobId: j.JobId}); status.Code(err) != codes.FailedPrecondition {
		t.Fatal(err)
	}
}
func TestGRPCDisallowedInputAndCancellationPropagation(t *testing.T) {
	e, r := fakeEngine(t)
	started := make(chan struct{})
	stopped := make(chan struct{})
	r.run = func(ctx context.Context, _ *Job, _ *Attempt) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}
	client := testRPC(t, e)
	ctx := context.Background()
	for _, req := range []*pb.SubmitRequest{{IdempotencyKey: "path", CandidatePath: "../gold.sv", CandidateSource: []byte("rtl")}, {IdempotencyKey: "bad", CandidatePath: CandidatePath, CandidateSource: []byte("reject")}} {
		if _, err := client.Submit(ctx, req); status.Code(err) != codes.InvalidArgument {
			t.Fatal(err)
		}
	}
	j, err := client.Submit(ctx, request("cancel"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker not started")
	}
	cancelled, err := client.Cancel(ctx, &pb.GetRequest{JobId: j.JobId})
	if err != nil || cancelled.State != pb.JobState_CANCELLED || cancelled.Eligible {
		t.Fatal(cancelled, err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not reach worker")
	}
	if _, err = client.Cancel(ctx, &pb.GetRequest{JobId: j.JobId}); err != nil {
		t.Fatal(err)
	}
}
func TestGRPCTransientRetryAndNonpassingEvidencePolicy(t *testing.T) {
	for _, mode := range []string{"retry", "counterexample", "missing"} {
		t.Run(mode, func(t *testing.T) {
			e, r := fakeEngine(t)
			calls := 0
			r.run = func(_ context.Context, j *Job, a *Attempt) error {
				calls++
				if mode == "retry" && calls == 1 {
					return errors.New("transient unavailable")
				}
				outcome := "passed"
				omit := ""
				if mode == "counterexample" {
					outcome = mode
				}
				if mode == "missing" {
					omit = "formal/PASS"
				}
				return r.emit(j, a, outcome, omit)
			}
			client := testRPC(t, e)
			j, err := client.Submit(context.Background(), request(mode))
			if err != nil {
				t.Fatal(err)
			}
			result := terminal(t, client, j.JobId)
			if mode == "retry" {
				if !result.Eligible || len(result.Attempts) != 2 || result.Attempts[0].Outcome != "infrastructure_error" {
					t.Fatal(result)
				}
			} else {
				if result.Eligible || len(result.Attempts) != 1 {
					t.Fatal(result)
				}
				if _, err = client.QueueReview(context.Background(), &pb.GetRequest{JobId: j.JobId}); status.Code(err) != codes.FailedPrecondition {
					t.Fatal(err)
				}
			}
		})
	}
}
func TestEvidenceIdentityHashAndRequiredGates(t *testing.T) {
	for _, change := range []string{"job", "candidate", "gate", "hash", "unknown-version", "unknown-field"} {
		t.Run(change, func(t *testing.T) {
			e, r := fakeEngine(t)
			j := mustSubmit(t, e.Store, "proof")
			_, a := claim(t, e.Store)
			if err := r.emit(j, a, "passed", ""); err != nil {
				t.Fatal(err)
			}
			root := r.AttemptRoot(j.ID, a.ID)
			path := filepath.Join(root, "manifest.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err = json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "job":
				m["job_id"] = "wrong"
			case "candidate":
				m["inputs"].(map[string]any)[CandidatePath].(map[string]any)["sha256"] = "wrong"
			case "gate":
				delete(m["gates"].(map[string]any), "equivalence")
			case "hash":
				if err = os.WriteFile(filepath.Join(root, "artifacts/formal/PASS"), []byte("modified"), 0600); err != nil {
					t.Fatal(err)
				}
			case "unknown-version":
				m["schema_version"] = 2
			case "unknown-field":
				m["eligible"] = true
			}
			raw, _ = json.Marshal(m)
			if err = os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if result, _, err := e.Validator.Check(j, a, root); err == nil || result.Eligible {
				t.Fatalf("accepted %s", change)
			}
		})
	}
}

func TestRecoveryCleansAbandonedUnitsAndKeepsTerminalResults(t *testing.T) {
	e, r := fakeEngine(t)
	j := mustSubmit(t, e.Store, "recover")
	_, old := claim(t, e.Store)
	if err := e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	stops := append([]string(nil), r.stops...)
	r.mu.Unlock()
	if len(stops) != 1 || stops[0] != old.ID {
		t.Fatal("abandoned service not cleaned before scheduling", stops)
	}
	recovered, _ := e.Store.Get(j.ID)
	if recovered.State != pb.JobState_QUEUED || recovered.Attempts[0].State != pb.AttemptState_ABANDONED {
		t.Fatal(recovered)
	}
	if _, err := e.Store.QueueReview(j.ID, ""); !errors.Is(err, ErrIneligible) {
		t.Fatal(err)
	}
}

func TestCleanupFailureStopsFurtherDispatch(t *testing.T) {
	e, r := fakeEngine(t)
	mustSubmit(t, e.Store, "one")
	mustSubmit(t, e.Store, "two")
	r.stopErr = errors.New("cleanup could not be verified")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := e.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Run(ctx); err == nil {
		t.Fatal("continued dispatch after cleanup failure")
	}
	e.Store.mu.Lock()
	defer e.Store.mu.Unlock()
	attempts := 0
	for _, j := range e.Store.data.Jobs {
		attempts += len(j.Attempts)
	}
	if attempts != 1 {
		t.Fatal("launched more work after cleanup failure", attempts)
	}
}
