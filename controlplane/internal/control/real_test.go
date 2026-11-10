package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	pb "github.com/Jeanm2005/forge-rtl/controlplane/api/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type daemon struct {
	cmd        *exec.Cmd
	done       chan error
	output     *boundedBuffer
	connection *grpc.ClientConn
	client     pb.ControlClient
	waited     bool
}

func startDaemon(t *testing.T, root, state string) *daemon {
	t.Helper()
	d := &daemon{done: make(chan error, 1), output: &boundedBuffer{max: 32768}}
	d.cmd = exec.Command(filepath.Join(root, "build/forge-control"), "-root", root, "-state", state, "-worker", filepath.Join(root, "worker/target/debug/forge-worker"), "-toolchain", filepath.Join(root, ".tools/oss-cad-suite"))
	d.cmd.Stdout = d.output
	d.cmd.Stderr = d.output
	if err := d.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { d.done <- d.cmd.Wait() }()
	socket := filepath.Join(state, "control.sock")
	deadline := time.Now().Add(35 * time.Second)
	for {
		if strings.Contains(d.output.Value(), socket+"\n") {
			break
		}
		select {
		case err := <-d.done:
			d.waited = true
			t.Fatalf("daemon startup: %v: %s", err, d.output.Value())
		default:
		}
		if time.Now().After(deadline) {
			d.cmd.Process.Kill()
			<-d.done
			d.waited = true
			t.Fatalf("daemon startup timed out: %s", d.output.Value())
		}
		time.Sleep(20 * time.Millisecond)
	}
	conn, err := grpc.NewClient("unix://"+socket, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(10*1024*1024)))
	if err != nil {
		t.Fatal(err)
	}
	d.connection = conn
	d.client = pb.NewControlClient(conn)
	t.Cleanup(func() { d.stop(t, false) })
	return d
}
func (d *daemon) stop(t *testing.T, crash bool) {
	t.Helper()
	if d.waited {
		return
	}
	d.connection.Close()
	if crash {
		_ = d.cmd.Process.Kill()
	} else {
		_ = d.cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case err := <-d.done:
		d.waited = true
		if err != nil && !crash {
			t.Errorf("daemon shutdown: %v: %s", err, d.output.Value())
		}
	case <-time.After(45 * time.Second):
		_ = d.cmd.Process.Kill()
		<-d.done
		d.waited = true
		t.Error("daemon cleanup deadline exceeded")
	}
}
func waitReal(t *testing.T, client pb.ControlClient, id string) *pb.Job {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	for {
		j, err := client.Get(ctx, &pb.GetRequest{JobId: id})
		if err != nil {
			t.Fatal(err)
		}
		if j.State != pb.JobState_QUEUED && j.State != pb.JobState_RUNNING {
			return j
		}
		select {
		case <-ctx.Done():
			t.Fatal("real job did not finish")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
func activeReal(t *testing.T, client pb.ControlClient, id string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		j, err := client.Get(context.Background(), &pb.GetRequest{JobId: id})
		if err != nil {
			t.Fatal(err)
		}
		if j.State == pb.JobState_RUNNING && len(j.Attempts) > 0 {
			a := j.Attempts[len(j.Attempts)-1]
			out, _ := exec.Command("/usr/bin/systemctl", "--user", "is-active", evalUnit(a.AttemptId)).Output()
			if strings.TrimSpace(string(out)) == "active" {
				return a.AttemptId
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("worker evaluation unit did not become active")
	return ""
}
func stoppedReal(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		stopped := true
		for _, unit := range []string{launchUnit(id), evalUnit(id)} {
			out, _ := exec.Command("/usr/bin/systemctl", "--user", "show", "--property=MainPID", "--value", unit).Output()
			value := strings.TrimSpace(string(out))
			if value != "" && value != "0" {
				stopped = false
			}
			groupBytes, _ := exec.Command("/usr/bin/systemctl", "--user", "show", "--property=ControlGroup", "--value", unit).Output()
			group := strings.TrimSpace(string(groupBytes))
			if group != "" {
				if processes, err := os.ReadFile(filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(group, "/"), "cgroup.procs")); err == nil && strings.TrimSpace(string(processes)) != "" {
					stopped = false
				}
			}

		}
		if stopped {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("service process tree still running", id)
}
func archiveState(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		t.Error(err)
	}
}

func TestActualWorkerGRPC(t *testing.T) {
	if os.Getenv("FORGE_REAL_TOOLS") != "1" {
		t.Skip("set FORGE_REAL_TOOLS=1; requires built binaries, host user systemd, and pinned EDA suite")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	state, err := os.MkdirTemp("/tmp", "forge-c3-")
	if err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(root, "build/control-integration", fmt.Sprint(time.Now().UnixNano()))
	defer os.RemoveAll(state)
	defer func() { archiveState(t, state, evidence); t.Log("durable gRPC evidence:", evidence) }()
	d := startDaemon(t, root, state)
	defer func() { d.stop(t, false) }()
	source, err := os.ReadFile(filepath.Join(root, CandidatePath))
	if err != nil {
		t.Fatal(err)
	}
	submit := func(key string, src []byte, timeout, attempts uint32) *pb.Job {
		t.Helper()
		j, err := d.client.Submit(context.Background(), &pb.SubmitRequest{IdempotencyKey: key, CandidatePath: CandidatePath, CandidateSource: src, TimeoutSeconds: timeout, MaxAttempts: attempts})
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	good := submit("good", source, 180, 1)
	goodResult := waitReal(t, d.client, good.JobId)
	if !goodResult.Eligible || goodResult.Outcome != "passed" {
		t.Fatal(goodResult)
	}
	recorded, err := d.client.GetEvidence(context.Background(), &pb.EvidenceRequest{JobId: good.JobId})
	if err != nil || digest(recorded.ManifestJson) != recorded.GetManifestSha256() {
		t.Fatal("gRPC evidence retrieval", err)
	}
	var manifest Manifest
	if err = json.Unmarshal(recorded.ManifestJson, &manifest); err != nil || manifest.Cells["gold"] != 988 || manifest.Cells["candidate"] != 988 {
		t.Fatal("baseline not reproduced", err)
	}
	review, err := d.client.QueueReview(context.Background(), &pb.GetRequest{JobId: good.JobId})
	if err != nil || review.State != pb.JobState_AWAITING_REVIEW {
		t.Fatal(review, err)
	}
	mutant, err := os.ReadFile(filepath.Join(root, "tests/mac_mutant.sv"))
	if err != nil {
		t.Fatal(err)
	}
	bad := submit("mutant", []byte(strings.Replace(string(mutant), "module mac_mutant", "module mac_candidate", 1)), 180, 1)
	badResult := waitReal(t, d.client, bad.JobId)
	if badResult.Eligible || badResult.Outcome != "counterexample" {
		t.Fatal(badResult)
	}
	if _, err = d.client.QueueReview(context.Background(), &pb.GetRequest{JobId: bad.JobId}); status.Code(err) != codes.FailedPrecondition {
		t.Fatal("mutant entered review", err)
	}
	negative, err := d.client.GetEvidence(context.Background(), &pb.EvidenceRequest{JobId: bad.JobId})
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(negative.ManifestJson, &manifest); err != nil || manifest.Gates["reachable_depth_3"].Outcome != "counterexample" {
		t.Fatal("mutant witness missing", err)
	}
	cancelled := submit("cancel", source, 180, 1)
	cancelAttempt := activeReal(t, d.client, cancelled.JobId)
	cancelResult, err := d.client.Cancel(context.Background(), &pb.GetRequest{JobId: cancelled.JobId})
	if err != nil || cancelResult.State != pb.JobState_CANCELLED {
		t.Fatal(cancelResult, err)
	}
	stoppedReal(t, cancelAttempt)
	restart := submit("restart", source, 180, 2)
	oldAttempt := activeReal(t, d.client, restart.JobId)
	d.stop(t, true)
	d = startDaemon(t, root, state)
	recovered := waitReal(t, d.client, restart.JobId)
	if !recovered.Eligible || len(recovered.Attempts) != 2 || recovered.Attempts[0].State != pb.AttemptState_ABANDONED || recovered.Attempts[1].AttemptId == oldAttempt {
		t.Fatal("restart recovery", recovered)
	}
	stoppedReal(t, oldAttempt)
	replay := submit("restart", source, 180, 2)
	if replay.JobId != restart.JobId {
		t.Fatal("restart lost idempotency")
	}
	timed := submit("deadline", source, 2, 1)
	timedResult := waitReal(t, d.client, timed.JobId)
	if timedResult.State != pb.JobState_TIMED_OUT || timedResult.Eligible {
		t.Fatal(timedResult)
	}
	if len(timedResult.Attempts) > 0 {
		stoppedReal(t, timedResult.Attempts[0].AttemptId)
	}
}
