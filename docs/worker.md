# Local Rust evaluation worker

Phase 2 executes the reviewed MAC flow in isolated attempts and publishes schema
v1 evidence. It has no scheduler, retry policy, eligibility decision, or promotion
operation. These belong to the later Go control plane and human review.

## Run and verify

Requirements: Linux x86-64, cgroup v2 with delegated memory/CPU/task controllers,
a working user systemd manager, Bubblewrap supporting `--size` and
`--disable-userns`, Rust with rustfmt/Clippy, Python 3, awk, GNU coreutils/tar,
G++, Make, and Node 18+ at `/usr/bin/node`. Install the pinned EDA suite with
`make setup`. The worker exposes `/usr` and the chosen toolchain, so tools
installed only in an operator's home or a hosted tool cache are not available.
The full test command requires host namespace and user-service permissions.
Unsupported hosts fail; the worker never falls back to unisolated execution.

On Ubuntu 24.04, the host administrator may need to permit Bubblewrap user
namespaces through an AppArmor profile. CI loads `scripts/ci-bwrap.apparmor`
on its disposable runner, scoped to `/usr/bin/bwrap`; namespace and cgroup
enforcement remains active. This follows the profile mechanism in the
[Ubuntu release notes](https://documentation.ubuntu.com/release-notes/24.04/#unprivileged-user-namespace-restrictions).
The local check command does not alter host AppArmor policy.

```sh
make worker-check
mkdir -p build/attempts
cargo run --manifest-path worker/Cargo.toml --locked -- evaluate \
  . build/attempts local-job attempt-1 rtl/candidate/mac_candidate.sv \
  .tools/oss-cad-suite
```

`stage TRUSTED_ROOT ATTEMPTS_ROOT JOB_ID ATTEMPT_ID CANDIDATE_FILE` performs
validation and staging only. `evaluate` takes the same arguments followed by the
trusted toolchain root and an optional limits JSON file. The CLI prints the
terminal manifest path. Exit 0 means a completed evaluation (including verification
failure, counterexample, or inconclusive); exit 2 means timeout, cancellation,
output overflow, or infrastructure failure. Exit 1 means invalid input or an
I/O failure preventing completion. Consumers must read the manifest outcome.
SIGINT/SIGTERM request cancellation and wait for service process-tree cleanup.

`make worker-check` runs formatting, Clippy, shell syntax checks, and all tests,
including host-only tests and real good/mutant evaluations. No tests are skipped.
Results are in `build/worker-check.log`, `build/worker-status`, and uniquely named
`build/worker-integration/<run>/` directories. Plain `cargo test` runs portable
checks and explicitly skips the marked host tests.

## Inputs and identity

Job and attempt identities are distinct and contain 1–64 ASCII letters, digits,
underscores, or hyphens. An attempt directory is named
`<job-length>-<job>-<attempt>` to avoid ambiguous identities. Directories are
created exclusively and never reused, including after staging failure. An
`EVALUATING` marker prevents concurrent evaluation or replacement of a terminal
manifest. A crashed attempt requires a new execution identity.

Only a full-file replacement of `rtl/candidate/mac_candidate.sv` is accepted.
The source is nonempty UTF-8, at most 1 MiB, and defines exactly one module named
`mac_candidate`. The initial supported subset forbids NULs, preprocessing,
strings, escaped identifiers, comments, attributes, initialization/finalization,
DPI/import/export/bind, procedural force, and formal statements. System functions
are limited to `$signed`, `$unsigned`, `$clog2`, and `$bits`. In particular,
Verilator comment directives, `$system`, `$c`, and candidate proof assumptions
are rejected before any tool executes. This intentionally conservative subset
can be expanded through separate trusted review; it is not a complete SV parser.
Tool frontends still validate syntax and synthesizability.

Trusted roots, tool installations, and the attempt parent are administrator-owned
configuration, stable during execution and inaccessible to proposers. Candidate
submissions cannot choose these roots or edit worker code. Fixed allowlisted
trusted files are staged with read-only permissions, with symlinks and traversal
rejected in their relative paths. Only staged inputs enter the sandbox. This
local API does not defend against concurrent privileged filesystem mutation or
vulnerabilities in the trusted kernel/toolchain. Reference, harnesses, scripts,
and numeric/proof contracts are reviewed inputs, never candidate replacements.

## Execution boundary

Each attempt runs in a transient user systemd service and a Bubblewrap sandbox.
The service cgroup enforces aggregate memory/task limits, CPU bandwidth, and
process-tree termination. Bubblewrap unshares user, PID, IPC, UTS, cgroup, and
network namespaces, disables nested user namespaces, drops capabilities, and
clears the environment. No host home, runtime sockets, credentials, or Git data
are mounted. System binaries/libraries, OS release/linker cache, the resolved awk
alternatives target, staged inputs, and EDA installation are mounted read-only.
The root and device directory are read-only; fresh `/proc` and device nodes serve
the sandbox. Writable storage is confined to bounded `/work/build` and `/tmp`
tmpfs mounts. The worker invokes a fixed script using explicit arguments.

Default limits (all are recorded in each manifest):

| Resource | Limit |
| --- | --- |
| Wall time | 900 seconds, plus a 5-second service backup deadline |
| Aggregate memory / swap | 2 GiB / 0 |
| Aggregate tasks | 128 |
| CPU bandwidth | 200%, with 240 CPU seconds per process |
| Build / temporary tmpfs | 512 MiB / 64 MiB |
| Individual file / open descriptors / core | 64 MiB / 256 / 0 |
| Artifact transport / console capture | 128 MiB / 8 MiB |
| Extracted artifacts | 4096 entries, 128 MiB total, 64 MiB per file |

An optional limits JSON specifies every field of `sandbox::Limits`; unknown or
out-of-range fields are rejected. Limits affect identity through a SHA-256 of
the serialized configuration. Comparisons require identical settings and trusted
input hashes. The host stores at most the bounded transport, a bounded extracted
copy, logs, inputs, and manifest per attempt; retention across attempts remains
an operator responsibility. Stage durations use monotonic time.

## Gates and schema v1

The fixed runner records tool versions, simulation, sequential EQY induction,
EQY match/partition audit, independent reachable depth-3 SAT, and generic Yosys
synthesis. Every applicable gate must explicitly pass for overall `passed`.
A passing bounded SAT check never substitutes for completed induction.
EQY counterexamples require matching terminal exit/marker, completed failing
partitions, public output matches, and nonempty traces. Bounded counterexamples
require the explicit SAT result and retained VCD/JSON. Synthesis counts must
agree between statistics, netlists, and comparison JSON. Counts are structural
cells, not physical area or verified optimization gains.

`manifest.json` contains schema version, job/attempt IDs, recording timestamp,
normalized outcome and diagnostic, limits/configuration hash, worker executable
hash, exact input hashes, toolchain release/archive identity and tool/solver
versions, seed/strategy, per-gate exits/durations/depth, generic counts, execution
unit/exit/duration/service result, and artifact/transport hashes and sizes.
Artifact map keys are relative to `<attempt>/artifacts`; input map keys are
relative to `<attempt>/inputs`. Raw transport is `<attempt>/artifacts.tar` and
`execution.log`. The worker executable hash identifies the fixed command and
parser implementation; the input hashes identify the reviewed flow/harnesses.

Normalized outcomes are `passed`, `verification_failed`, `counterexample`,
`inconclusive`, `timeout`, `cancelled`, `infrastructure_error`, and `output_limit`.
Malformed/missing/contradictory reports are infrastructure failures. A solver
failure to prove is inconclusive, never a completed counterexample. OOM and tool
errors remain infrastructure failures with the original service result retained.
Missing prerequisites and incomplete evidence cannot produce `passed`.

Raw tar is untrusted: extraction rejects traversal, symlinks, hard links,
special files, duplicates, and size/path/count overruns. Extracted files and
directories are synced and renamed into `artifacts` before the synced terminal
manifest is atomically renamed into place. A failure can leave bounded raw or
`.pending` diagnostics. There is no terminal result if publication itself fails.
On hard worker death, the service backup deadline bounds remaining execution;
recovery belongs to the future control plane. Cancellation and normal deadlines
actively stop the entire service, wait for process/pipe cleanup, and publish a
non-passing terminal outcome. Counterexample traces are retained on completed
negative runs; abrupt termination can interrupt the final artifact transport.
