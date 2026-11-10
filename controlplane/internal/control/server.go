package control

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	pb "github.com/Jeanm2005/forge-rtl/controlplane/api/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	pb.UnimplementedControlServer
	Engine *Engine
}

func rpcError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, ErrConflict):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, ErrIneligible), errors.Is(err, ErrStale):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	default:
		return status.Error(codes.Unavailable, err.Error())
	}
}
func wire(j *Job) *pb.Job {
	result := &pb.Job{JobId: j.ID, State: j.State, CreatedUnixMs: j.Created, DeadlineUnixMs: j.Deadline, MaxAttempts: j.MaxAttempts, CandidateSha256: j.SourceSHA, Eligible: j.Eligible, Outcome: j.Outcome, Diagnostic: j.Diagnostic}
	for _, a := range j.Attempts {
		result.Attempts = append(result.Attempts, &pb.Attempt{AttemptId: a.ID, State: a.State, LeaseUntilUnixMs: a.LeaseUntil, Outcome: a.Outcome, Diagnostic: a.Diagnostic, ManifestSha256: a.ManifestSHA})
	}
	return result
}
func (s *Server) Submit(ctx context.Context, req *pb.SubmitRequest) (*pb.Job, error) {
	if req == nil || req.CandidatePath != CandidatePath || len(req.CandidateSource) == 0 || len(req.CandidateSource) > MaxSource || !utf8.Valid(req.CandidateSource) || len(req.IdempotencyKey) == 0 || len(req.IdempotencyKey) > 128 {
		return nil, status.Error(codes.InvalidArgument, "allowlisted candidate path, bounded UTF-8 source, and 1..128 byte idempotency key required")
	}
	copy := &pb.SubmitRequest{IdempotencyKey: req.IdempotencyKey, CandidatePath: req.CandidatePath, CandidateSource: append([]byte(nil), req.CandidateSource...), TimeoutSeconds: req.TimeoutSeconds, MaxAttempts: req.MaxAttempts}
	if copy.TimeoutSeconds == 0 {
		copy.TimeoutSeconds = 1800
	}
	if copy.MaxAttempts == 0 {
		copy.MaxAttempts = 2
	}
	if copy.TimeoutSeconds > 3600 || copy.MaxAttempts > 3 {
		return nil, status.Error(codes.InvalidArgument, "timeout must be 1..3600 seconds and max_attempts 1..3")
	}
	if replay, err := s.Engine.Store.Replay(copy); err != nil {
		return nil, rpcError(err)
	} else if replay != nil {
		if replay.Eligible && s.eligible(replay) != nil {
			replay.Eligible = false
		}
		return wire(replay), nil
	}
	if err := s.Engine.Runner.Validate(ctx, copy.CandidateSource); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, rpcError(err)
		}
		if errors.Is(err, ErrCandidateInvalid) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, rpcError(err)
	}
	job, err := s.Engine.Store.Submit(copy)
	if err != nil {
		return nil, rpcError(err)
	}
	s.Engine.Notify()
	return wire(job), nil
}
func (s *Server) Get(_ context.Context, req *pb.GetRequest) (*pb.Job, error) {
	job, err := s.Engine.Store.Get(req.GetJobId())
	if err != nil {
		return nil, rpcError(err)
	}
	if job.Eligible {
		if err := s.eligible(job); err != nil {
			job.Eligible = false
			job.Diagnostic = "evidence unavailable: " + err.Error()
		}
	}
	return wire(job), nil
}
func (s *Server) Cancel(_ context.Context, req *pb.GetRequest) (*pb.Job, error) {
	job, err := s.Engine.Store.Cancel(req.GetJobId())
	if err != nil {
		return nil, rpcError(err)
	}
	for _, a := range job.Attempts {
		if a.State == pb.AttemptState_ATTEMPT_CANCELLED {
			s.Engine.CancelAttempt(a.ID)
		}
	}
	return wire(job), nil
}
func (s *Server) GetEvidence(_ context.Context, req *pb.EvidenceRequest) (*pb.Evidence, error) {
	evidence, err := s.Engine.Evidence(req.GetJobId(), req.GetAttemptId())
	if err != nil {
		return nil, rpcError(err)
	}
	return evidence, nil
}
func (s *Server) eligible(job *Job) error {
	if !job.Eligible || len(job.Attempts) == 0 {
		return ErrIneligible
	}
	a := job.Attempts[len(job.Attempts)-1]
	evidence, err := s.Engine.Evidence(job.ID, a.ID)
	if err != nil {
		return err
	}
	result, raw, err := s.Engine.Validator.Check(job, a, s.Engine.Runner.AttemptRoot(job.ID, a.ID))
	if err != nil {
		return err
	}
	if !result.Eligible || digest(raw) != evidence.ManifestSha256 {
		return fmt.Errorf("%w: evidence changed", ErrIneligible)
	}
	return nil
}
func (s *Server) QueueReview(_ context.Context, req *pb.GetRequest) (*pb.Job, error) {
	job, err := s.Engine.Store.Get(req.GetJobId())
	if err != nil {
		return nil, rpcError(err)
	}
	if err = s.eligible(job); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	job, err = s.Engine.Store.QueueReview(job.ID, job.Attempts[len(job.Attempts)-1].ManifestSHA)
	if err != nil {
		return nil, rpcError(err)
	}
	return wire(job), nil
}
