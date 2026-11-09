# Development record

## Current snapshot

| Field | State |
| --- | --- |
| Active phase | Phase 1 complete; Phase 2 in progress |
| Current target | Signed INT8 × INT8 MAC with wrapping INT32 accumulation |
| Last verified revision | `2bb50e0` (full SHA in `build/ci-verified/revision.txt`) |
| Tested environment | Ubuntu 24.04 GitHub Actions, pinned OSS CAD Suite 2026-11-05 |
| Current baseline | 273,187 simulation cycles; completed proof; reachable mutant counterexample; 988 generic cells per implementation |
| Next action | Add sandboxed execution, supervision, and evidence to Rust staging |
| Open question | No Phase 1 blockers remain |

## Baseline results

Evidence below is extracted from CI run [42083661284](https://github.com/Jeanm2005/forge-rtl/actions/runs/42083661284)
into ignored `build/ci-verified/`.

| Check | Observed result | Evidence relative to that directory |
| --- | --- | --- |
| Verilator simulation | PASS: 273187 cycles, seed 1badf00d, one wrap each direction | `sim/result.log` |
| EQY equivalence | PASS: public ports only, one completed partition | `formal/PASS`, `formal/matched.ids`, `formal/strategies/mac_formal.acc/induction/` |
| Negative control | EQY FAIL with confirmed partition FAIL; reachable depth-3 SAT counterexample | `negative/eqy/`, `negative/counterexample.vcd`, `negative/counterexample.json`, `negative/reachable.log` |
| Generic Yosys synthesis | Gold 988, candidate 988, delta 0; both netlist formats emitted | `synth/comparison.json`, `synth/{gold,candidate}/` |
| Full suite | PASS from clean Actions checkout | `status`, `all.log`, `all-inputs.txt`, `versions.txt`, `revision.txt` |
| Runner syntax | PASS | Actions syntax step |

## Engineering decisions

| ID | Decision | Rationale |
| --- | --- | --- |
| D001 | Start with a single MAC and fixed arithmetic/reset contract | Establish a small reproducible baseline. |
| D002 | Keep a signedness mutant as a permanent negative control | Demonstrate observable signed-arithmetic sensitivity. |
| D003 | Introduce Rust execution before Go orchestration and Python proposals | Deferred to later phases per roadmap. |
| D004 | Use EQY SBY smtbmc Bitwuzla induction, depth 10, from the pinned suite | SAT strategy returns UNKNOWN for the mutant; SBY supplies explicit PASS/FAIL and counterexample evidence. |
| D005 | Disable internal EQY matches and audit public-only matches | Prevent internal cut points from hiding a sequential reset or arithmetic mismatch. Require every generated partition's strategy to pass. |
| D006 | Confirm mutant failure independently with bounded reachable SAT, depth 3 | EQY failure alone may mean failure to prove. Require an actual model and retained VCD/JSON; reject errors/timeouts. |
| D007 | Keep candidate as an equivalence fixture | No measurements support an optimization claim. |

## Run history

### 2026-11-05 · phase1-bootstrap (blocked)

- Revision: unavailable. This supplied directory contains an empty, read-only
  `.git/`, not a usable Git checkout. Existing project documents were read before
  implementation. Source/configuration identities are SHA-256 records in
  `build/*-inputs.txt`; a clean Git checkout run remains outstanding.
- Scope: separate gold/candidate SV; test-only zero-extension mutant; independent
  simulation scoreboard; trusted first-reset formal wrapper and bounded mutant
  miter; same generic Yosys synthesis flow for both designs. No later-phase
  services, model integration, or physical design.
- Environment: Ubuntu 26.04.1 LTS (Resolute Raccoon), WSL2,
  `Linux 6.18.54.1-microsoft-standard-WSL2`, x86-64. Available GNU Make 4.4.1,
  G++ 15.2.0, Node.js v24.22.1. Verilator/Yosys/EQY and external SMT solvers are
  not installed. No tested HDL tool or solver version can be reported.
- Discovery: `command -v verilator yosys eqy sby boolector z3 make` found only
  Make. `apt-cache policy verilator yosys boolector z3` showed package candidates,
  not installed tools. No EDA packages were found in the local apt archive.
  `curl -I -L --max-time 15 https://github.com/YosysHQ/eqy` failed with exit 6,
  `Could not resolve host: github.com`; downloading prerequisites is blocked.
- Exact final working-tree commands:

  ```sh
  make clean
  make check
  for target in sim formal negative synth; do make "$target"; done
  bash -n scripts/check.sh
  node --check scripts/counts.mjs
  node --check scripts/audit-eqy.mjs
  ```

- `make check` and each EDA target returned Make exit 2 after prerequisite
  failures; underlying runner exit 1. Syntax commands returned 0. Logs identify
  the missing prerequisite for each check. No old artifacts satisfied a rerun.
- Fresh source-only directory attempt:

  ```sh
  mkdir -p /tmp/forge-rtl-phase1-clean
  cp -r AGENTS.md README.md ROADMAP.md DEVELOPMENT.md Makefile .gitignore rtl tests formal scripts /tmp/forge-rtl-phase1-clean/
  make -C /tmp/forge-rtl-phase1-clean check
  ```

  This also returned Make exit 2 at the prerequisite gate. This is a clean
  source-directory attempt, not a claim of a clean Git checkout validation.
  Evidence: `/tmp/forge-rtl-phase1-clean/build/all.log` and `all-inputs.txt`.
- Intended simulation seed: `0x1badf00d`, 10000 xorshift32 stimulus cycles plus
  directed cases and legal long accumulation sequences for both wrap directions.
  Actual exercised simulation cycles: none, because compilation is blocked.
- Intended proof: EQY `sat` k-induction depth 10, Yosys built-in SAT solver,
  180 s wall limit. Trusted first transition forces shared reset, pre-reset
  observation masked; later reset, enable and operands unconstrained. Intended
  negative witness: bounded SAT depth 3, 180 s, post-reset mismatch. Actual proof
  and counterexample results: unavailable; no traces produced.
- Intended synthesis: `hierarchy -check -top`, `synth -top`, `check -assert`,
  `stat -json`; identical generic flow, no technology mapping library. Intended
  results: `build/synth/{gold,candidate}/{netlist.v,netlist.json,stat.json,yosys.log}`
  and `build/synth/comparison.json`. Actual counts: gold unavailable, candidate
  unavailable, delta unavailable. These are generic cells, not physical area.
- Interpretation: Phase 1 is **not complete**. Next run must establish tool
  versions, fix any real-tool incompatibilities, inspect EQY matching/partition
  evidence and witness, and record actual simulation/proof/count outcomes.

## Open issues

No Phase 1 blockers remain. Historical blocked attempts below are retained as
observations from their original environments.

### 2026-11-05 · Repository publication and CI bootstrap (publication blocked)

- Target repository: `https://github.com/Jeanm2005/forge-rtl.git`, default branch
  `main`. Connected GitHub read access confirms it exists and has no branches.
- The supplied workspace's `.git/` is read-only and empty. A separate real local
  repository is prepared at `/tmp/forge-rtl-publish`, with feature commits and
  `origin` pointing at the target. The final history is also saved as
  `build/forge-rtl.bundle`. Workspace source changes are preserved.
- Local commit author and committer: configured name `Jean Cruz`, account-linked
  noreply email `189168563+Jeanm2005@users.noreply.github.com`. No coauthor or bot
  author is added. Existing global Git configuration is unchanged. GitHub owns
  contribution attribution and dashboard display; same-day commits need not
  appear as separate dashboard activity cards.
- Publication attempts: `git -C /tmp/forge-rtl-publish push origin main` after
  each commit fails with exit 128, `Could not resolve host: github.com`. The
  connected GitHub integration's attempt to create `AGENTS.md` failed with HTTP
  403, `Resource not accessible by integration`. It created no remote commit.
  No claim of a successful push or remote workflow run is made. Repository
  collaborator administration is unavailable; no collaborators were added.
- CI implementation: `.github/workflows/phase1.yml`, triggered on push, pull
  request, or manual dispatch; Ubuntu 24.04 runner, Node 24.22.1, pinned action
  commit SHAs, read-only token, 25-minute job limit, full suite, verified evidence
  delivery after success, and failure diagnostics. No deployment or automatic
  promotion is appropriate for this Phase 1 RTL fixture.
- Requirements implementation: `make setup` invokes `scripts/setup.sh`; checks
  Linux x86-64, installs missing apt build/runtime prerequisites, downloads OSS
  CAD Suite release `2026-11-05`, verifies SHA-256
  `c64d557848d57fad96a5bea7461e12b3ddfb49514658f7301f7e1a5476128a88`,
  and writes `.tools/env.sh`. Release asset identity and digest were read from
  YosysHQ's GitHub release API. The toolchain is bundled; external SMT solvers
  are not required for the selected SAT strategy.
- Source review correction: upstream EQY's `src/eqy_job.py` documents exit 2 for
  a failed equivalence check (exit 16 for errors). The negative-control runner
  now requires exit 2, a FAIL marker, and completed PASS/FAIL partition outcomes
  with at least one confirmed failure. It still independently requires the
  reachable post-reset bounded counterexample and retained trace. No tool
  execution is implied by this source review.
- Exact checks:

  ```sh
  bash -n scripts/setup.sh scripts/check.sh
  node --check scripts/counts.mjs
  node --check scripts/audit-eqy.mjs
  actionlint .github/workflows/phase1.yml
  make setup
  make check
  ```

  Syntax and actionlint checks pass (exit 0). `make setup` reaches the pinned
  archive download, then fails with curl exit 6 / Make exit 2 after DNS retries.
  `make check` remains blocked by missing Verilator/Yosys/EQY. Phase 1 is still
  incomplete; no passing CI result or new cell measurement exists.
- Next action: restore shell GitHub network access and valid push credentials,
  or grant the connected integration repository/workflow write permissions.
  Push each local commit in sequence to `main`, inspect the first real Actions
  run, and record actual versions and outcomes before claiming a baseline.
- Clean Git clone attempt after assembling the history:

  ```sh
  git clone --no-hardlinks /tmp/forge-rtl-publish /tmp/forge-rtl-clean-validation
  make -C /tmp/forge-rtl-clean-validation check
  bash -n /tmp/forge-rtl-clean-validation/scripts/setup.sh /tmp/forge-rtl-clean-validation/scripts/check.sh
  node --check /tmp/forge-rtl-clean-validation/scripts/audit-eqy.mjs
  node --check /tmp/forge-rtl-clean-validation/scripts/counts.mjs
  actionlint /tmp/forge-rtl-clean-validation/.github/workflows/phase1.yml
  ```

  Clone and syntax/lint checks passed. Full suite returned Make exit 2 at the
  missing EDA prerequisites. Attempted revision was `a90fb2c` (the documentation
  commit before adding this observation; RTL and runner inputs are unchanged).
  Evidence: `/tmp/forge-rtl-clean-validation/build/all.log` and `all-inputs.txt`.
  A clean local Git clone is now available; a successful EDA run is still pending.

### 2026-11-05 · Publication reconciled and Phase 1 validated

- Preserved all nine original commits, `4595d6d` through `2fc457a`, and pushed
  each separately in order. Verified the original backup bundle with
  `git -C /tmp/forge-rtl-publish bundle verify /home/jeanm/havefun/forge-rtl/build/forge-rtl.bundle`.
  Remote was empty. `git fetch origin`, `git diff --cached origin/main --exit-code`,
  and `git switch -C main origin/main` reconciled the workspace without losing
  source changes. The original bundle and prepared checkout remain available.
- CI repairs are appended commits: scoped `[match mac_formal]` as required by
  EQY's parser, excluded preserved `dut` scope from matching, and replaced SAT
  strategy with SBY/Bitwuzla. The public-only audit and strict completed outcome
  requirements remain enforced. No RTL or proof assumptions changed.
- Clean Actions checkout at `2bb50e0`: `bash scripts/setup.sh`,
  `bash -n scripts/check.sh scripts/setup.sh`, `node --check scripts/counts.mjs`,
  `node --check scripts/audit-eqy.mjs`, then `source .tools/env.sh; make check`
  all exit 0. Run 42083661284 completed successfully and delivered the verified
  archive plus SHA-256. Earlier runs 42083029931, 42083299789, and 42083463134
  retain failure diagnostics; rapid initial pushes also cancelled run 42083030296.
- Observed tools: Verilator 5.055 devel `v5.054-63-g57bbb293d`, Yosys 0.70+336
  `8f3cc3d0c`, EQY v0.70-2-g3301516, SBY v0.70-9-g79fc9ed,
  Bitwuzla 0.9.1, GCC 13.3.0, Make 4.3, Node 24.22.1.
- Results and paths are in the current baseline table. Counts are generic
  structural cells, with no physical area or optimization claim. Phase 1 exit
  criteria are satisfied; no candidate has been promoted.

### 2026-11-09 · Phase 2 attempt staging

- Added dependency-free Rust library and CLI in `worker/` for typed job/attempt
  identities, bounded full-file candidate replacement, and exclusive attempt
  directories. Only `rtl/candidate/mac_candidate.sv` is replaceable. Candidate
  preprocessing is rejected conservatively. Trusted files are explicitly
  allowlisted; source symlinks are rejected. Input files are read-only.
- Staging is not a sandbox. No RTL or EDA tool is executed by the worker yet.
  Caller-controlled trusted roots must be stable and administrator-owned. The
  `PREPARED` marker reports staging only, never verification or eligibility.
- Exact checks (all exit 0):
  `cargo fmt --manifest-path worker/Cargo.toml`,
  `cargo test --manifest-path worker/Cargo.toml` (3 integration tests),
  `cargo clippy --manifest-path worker/Cargo.toml --all-targets -- -D warnings`.
  Tests cover disallowed paths, invalid identities, encoding/size/directive
  rejection, source symlinks, attempt separation, and overwrite refusal.
- Phase 2 remains incomplete. Next: enforce network/credential isolation and
  resource quotas before execution, supervise process trees and cancellation,
  parse tool outcomes, publish hashed/versioned evidence, and reproduce both
  baseline and mutant outcomes through the worker. Phase 1 RTL is unchanged.

### 2026-11-09 · Phase 2 sandbox and supervision

- Added Bubblewrap isolation inside a transient user systemd service. The worker
  clears environment variables, exposes only read-only system/tool/input mounts,
  unshares networking and process namespaces, disables nested user namespaces,
  and provides size-limited writable tmpfs mounts. Cgroups enforce aggregate
  memory and task limits; systemd also enforces CPU bandwidth, CPU/file/open-file
  limits, and a backup runtime deadline. Output capture is independently bounded.
- Cancellation, wall timeout, and output overflow stop the entire service control
  group; the worker waits for pipe drainage and records the service result.
  The current trusted runner only tests transport; EDA evaluation is next.
- Exact commands: `cargo fmt --manifest-path worker/Cargo.toml`,
  `cargo clippy --manifest-path worker/Cargo.toml --locked --all-targets -- -D warnings`,
  `cargo test --manifest-path worker/Cargo.toml --locked --test sandbox -- --ignored --test-threads=1`.
  All pass. Six host integration tests verify the isolation boundary, aggregate
  memory OOM, aggregate task limits, timeout/cancellation process-tree cleanup,
  output limits, and tool exit errors. These require host execution permission;
  the session's outer sandbox denies namespace creation. Unsupported hosts fail
  closed. No fallback to unsandboxed RTL execution exists.

### 2026-11-09 · Phase 2 trusted evaluation and evidence

- Added the fixed simulation/equivalence/reachable-SAT/synthesis runner and Rust
  result normalization. Preserved the reviewed Phase 1 scripts and assumptions;
  the added candidate reachability harness differs from the negative-control
  harness only by instantiating `mac_candidate` instead of `mac_mutant`.
- Evidence schema v1 records independent job/attempt IDs, source hashes, worker
  binary hash, resource configuration hash, tool/solver versions, strategy,
  simulation seed, gate exits/durations/outcomes, bounded depth, generic counts,
  artifact hashes/locations, and execution/cgroup result. Artifacts are validated,
  synced and published before atomic manifest publication. Re-evaluation cannot
  overwrite a terminal attempt. Malformed archives retain raw transport evidence.
- Candidate validation now limits submissions to one comment-free synthesizable
  MAC module, without strings, attributes, preprocessing, DPI, simulator commands,
  or formal statements. Only `$signed`, `$unsigned`, `$clog2`, and `$bits` system
  functions are allowed. This prevents candidate code from executing commands or
  adding assumptions inside the trusted shared tool flow.
- Initial real runs exposed an omitted awk alternatives target and a host
  wall-clock adjustment. Exposed only the resolved awk binary at its alternatives
  path and switched stage durations to monotonic time. Both failures were rejected
  as infrastructure errors; their original evidence remains under
  `build/worker-integration/1794256781383084255/` and
  `build/worker-integration/1794256987719031221/`.
- Exact final full-suite command:
  `cargo test --manifest-path worker/Cargo.toml --locked -- --include-ignored --test-threads=1 --nocapture`.
  Exit 0: 19 integration tests, including eight host-only sandbox/evidence/signal
  tests and one real-tool good/mutant test; none skipped. Formatting, Clippy with
  `-D warnings`, and `bash -n scripts/worker-run.sh` also pass.
- Verified evidence:
  `build/worker-integration/1794257094047379108/12-mac-baseline-{good,mutant}/manifest.json`.
  Good: all gates pass, 273187 cycles, completed induction, gold/candidate 988/988
  generic cells. Mutant: scoreboard mismatch, completed EQY counterexample and
  independent depth-3 VCD/JSON witness; gold/candidate 988/532 generic cells.
  Both attempts use identical trusted-input hashes, limits, toolchain and seed.
  Real integration verifies every published artifact hash.
