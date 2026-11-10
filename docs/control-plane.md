# Local Go gRPC control plane

Phase 3 adds durable submission, scheduling, status, cancellation, evidence
retrieval, and explicit queuing for human review. Go owns lifecycle and policy;
the Rust worker still owns input validation, staged execution, isolation,
resource enforcement, and tool-result normalization. No endpoint promotes RTL.

## Build, run, and verify

Use Go 1.26.10+, Rust with rustfmt/Clippy, and the sandbox/tool requirements in
[worker.md](worker.md). The full command runs Go formatting/vet/race checks, the
actual daemon over a Unix socket, real good/mutant worker evaluations, active
cancellation, hard-crash/restart recovery, deadline cleanup, and all Rust tests.
It enables every host integration test; unsupported hosts fail rather than skip.

```sh
make setup
make control-check
build/forge-control -root . -state build/control-state \
  -worker worker/target/debug/forge-worker -toolchain .tools/oss-cad-suite
```

Run clients in another shell. Flags precede the command:

```sh
build/forge-client -key baseline-1 submit
build/forge-client -id <job-id> get
build/forge-client -id <job-id> evidence
build/forge-client -id <job-id> review
build/forge-client -id <job-id> cancel
```

`submit` reads `rtl/candidate/mac_candidate.sv` by default; `-candidate FILE`
selects replacement bytes, not a different server-side path. `-timeout` specifies
the overall deadline in seconds; `-max-attempts` specifies the retry budget.
`-socket` selects a local socket; the default is
`build/control-state/control.sock`. Evidence output is the recorded manifest
JSON, checked against its RPC SHA-256. `-attempt` selects an earlier recorded
attempt; otherwise the latest attempt is used.

The daemon uses a Unix socket with mode 0600 inside operator-owned state created
with mode 0700. Only the local service account should access it. There is no TCP
listener, public authentication scheme, or remote worker enrollment in Phase 3.
Use a short state path to stay within the OS Unix-socket pathname limit. Trusted
repository, worker executable, EDA installation, and state roots are operator
configuration; RPC clients cannot set them. Do not expose these writable roots
to proposers. The daemon and its files assume stable, trusted local storage.

The check command writes `build/control-check.log`, `build/control-status`,
`build/control-integration/<run>/`, and Rust `build/worker-integration/<run>/`.
The real daemon test uses a short `/tmp/forge-c3-*` state root for its socket,
then copies its stopped durable state/evidence into the ignored build directory.
Absolute execution paths in copied manifests refer to the original run; inventory
keys remain relative and their recorded hashes still identify the copied files.

## RPC v1

The authoritative contract is [control.proto](../controlplane/api/v1/control.proto),
package `forge.control.v1`. Generated Go client/server bindings are checked in;
normal builds need no protobuf compiler. Breaking changes require a new RPC
version. Proposals submit a full-file replacement for the single allowlisted
candidate path. Reference, harnesses, assumptions, scripts, and settings remain
protected reviewed inputs.

| Method | Behavior |
| --- | --- |
| `Submit` | Validate bounded source with Rust, persist an idempotent logical job, return identity/status |
| `Get` | Return job and attempt history, current eligibility, outcomes, and diagnostics |
| `Cancel` | Durably fence queued/running work and request process-tree cleanup |
| `GetEvidence` | Return a previously accepted attempt's manifest bytes, SHA-256, and artifact directory |
| `QueueReview` | Revalidate passing evidence and durably mark the job awaiting human review |

Idempotency keys are 1–128 bytes. Repeating a key with the same normalized path,
bytes, deadline duration, and attempt budget returns the original job, including
after restart. Changing the request under that key returns `AlreadyExists`.
A known replay does not require another worker validation process. Candidate
source must be nonempty UTF-8 and at most 1 MiB, and must satisfy Rust's documented
restricted RTL subset. Invalid input returns `InvalidArgument`; unknown jobs or
unrecorded attempts return `NotFound`; unavailable/ineligible evidence and review
requests return `FailedPrecondition`; storage/service failures return
`Unavailable`. All RPC replies describe persisted decisions, except that `Get`
can lower its returned eligibility when previously recorded files are missing or
corrupted. Caller RPC cancellation does not cancel an already submitted job;
use `Cancel` for logical-job cancellation.

## Lifecycle, leases, retries, and restart

Each job has a random `j` identity, immutable source/hash, creation time, absolute
overall deadline, and maximum attempt count. Attempts have fresh random `a`
identities and independent leases, outcomes, and evidence references. Queued
jobs are ordered by creation time, then identity. The local dispatcher runs one
attempt at a time, and waits for cleanup before dispatching another.

| Policy | Value |
| --- | --- |
| Overall job deadline | Default 1800 seconds; requested range 1–3600 |
| Total attempt budget | Default 2; requested range 1–3 |
| Lease duration / heartbeat | 15 seconds / 5 seconds, capped by the job deadline |
| Scheduler tick | 100 ms |
| Retained logical jobs | 64 per state directory; operator manages archival/new directories |
| RPC source / receive / evidence reply | 1 MiB / 2 MiB / 10 MiB |
| Worker console capture | 8 KiB, continuously drained |

The job deadline includes queueing and retries and is never extended by a
heartbeat. An expired lease cannot renew itself. Completions must match the
current active attempt, unexpired lease, and unexpired job deadline at the final
state commit. Late results may leave diagnostic artifacts but cannot replace a
terminal result or grant eligibility. Cancellation and deadline transitions
fence attempts before cleanup, so a racing successful worker result cannot win.
`Cancel` acknowledges durable intent; cleanup finishes asynchronously. Cancelling
an already terminal job returns the unchanged terminal state.

Go retries worker infrastructure errors and process/transport failures within
the configured total attempt/deadline budget. Lease expiry and abandoned attempts
also consume that budget. Verification failure, counterexample, inconclusive,
worker timeout, output-limit, and malformed successful-worker evidence are not
retried. Missing evidence is never passing; a process failure with missing
evidence is retryable, while a successful process producing invalid evidence is
a permanent infrastructure result. Exhausted infrastructure retries terminate
ineligible. Every earlier attempt and accepted evidence reference is retained.

The daemon owns the state directory through an exclusive filesystem lock. Each
mutation writes a new versioned JSON snapshot, syncs it, renames it atomically,
and syncs the directory before acknowledging success. A failed/ambiguous write
poisons the writer until restart. Existing snapshots are structurally validated;
unknown versions and inconsistent identities/states are rejected. A policy hash
binds state to the trusted input hashes, worker binary, EDA archive identity, and
resource settings. Changed evaluation settings require a new state directory.
State/evidence retention is bounded by the job and attempt budgets; operators
must provision disk for the worker's documented per-attempt storage limits.

The Rust supervisor is launched in a known `forge-launch-<attempt>.service`;
its EDA sandbox runs as `forge-eval-<attempt>.service`. The launch service bounds
supervisor memory/tasks/runtime separately from the EDA service. On cancellation
or shutdown Go stops the launch service and then the EDA service, preventing a
surviving supervisor from recreating the EDA unit after cleanup. SIGTERM lets the
Rust worker use its established cancellation path. Worker console pipes are
bounded and drained. Cleanup failures stop further dispatch.

After a hard daemon crash, startup fences previously leased attempts as
abandoned, then stops **both** known units for every recorded attempt before
accepting RPCs or scheduling retries. Cleanup failure prevents startup, including
on a later restart after the attempt has already been marked abandoned. The new
attempt uses the same immutable job input and a fresh identity. A crash between
artifact publication and terminal persistence can leave orphaned diagnostics;
the restarted service re-evaluates within budget rather than trusting an
uncommitted completion. No worker completion RPC is exposed in this local phase.

## Evidence and review eligibility

Go independently checks worker schema v1 and job/attempt identity, the worker
executable hash, resource/configuration hash, expected seed/proof strategy,
pinned toolchain identity, exact reviewed input inventory, and candidate hash.
It validates every inventory path, rejects symlinks/traversal/special files,
streams file hashes under byte/count limits, and requires all recorded bytes to
match before accepting the evidence. Successful execution also requires explicit
success from tools, simulation, equivalence, match audit, depth-3 reachable SAT,
and synthesis; completed process/service status, tool versions, structural cell
counts, and required artifacts must be present. A bounded check never replaces
completed EQY induction. Rust remains responsible for strict tool-report parsing.

The worker publishes artifacts before its manifest. Go syncs the attempt directory
and its parent, publishes a separate immutable manifest copy under
`evidence/<attempt>/manifest.json`, then commits the terminal job/attempt state
and manifest SHA-256. `GetEvidence` serves that copy only after hash verification.
Review eligibility is a Go decision, not a worker field. `Get` and `QueueReview`
recheck the actual files; missing, changed, or non-passing evidence stays
ineligible. Queuing review is idempotent and retains the verified manifest
identity. It does not promote a patch or make an optimization claim.

Worker integration adds `forge-worker validate`, which reads bounded stdin and
returns `{"schema_version":1,"valid":true}` only after candidate validation,
without staging or executing RTL. The trusted `FORGE_CONTROLLED_UNIT` environment
setting may select exactly `forge-eval-<attempt>.service`; mismatched identities
are rejected. Candidate RTL does not receive this host setting. Standalone
Phase 2 execution keeps its original unique default service names.

## Regenerating bindings

Use protoc 33.0, protoc-gen-go 1.36.10, and protoc-gen-go-grpc 1.5.1:

```sh
protoc -I controlplane --go_out=controlplane --go_opt=paths=source_relative \
  --go-grpc_out=controlplane --go-grpc_opt=paths=source_relative \
  controlplane/api/v1/control.proto
```

The Phase 3 CI job pins Go and third-party actions, installs the same EDA suite
and scoped CI AppArmor profile, runs `make control-check`, scans Go dependencies
with govulncheck, and uploads durable job/attempt/evidence records plus test logs.
Future Phase 4 proposal generation consumes this interface; it does not acquire
write access to trusted evaluation inputs.
