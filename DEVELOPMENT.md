# Development record

## Current snapshot

| Field | State |
| --- | --- |
| Active phase | Phase 1 · Local MAC baseline, implementation present; validation blocked |
| Current target | Signed INT8 × INT8 MAC with wrapping INT32 accumulation |
| Last verified revision | No verified revision; supplied `.git/` is empty and `git rev-parse HEAD` fails |
| Tested environment | Ubuntu 26.04.1 LTS, WSL2 x86-64; shell/Node syntax checks only |
| Current baseline | Unmeasured; no simulation pass, completed proof, confirmed counterexample, or synthesis counts |
| Next action | Provision compatible Verilator/Yosys/EQY; run `make clean` then `make check` |
| Open question | Tool compatibility and all real EDA outcomes remain unverified |

## Baseline results

| Check | Observed result | Evidence |
| --- | --- | --- |
| Verilator simulation | BLOCKED: Verilator absent | `build/sim.log` |
| EQY equivalence | BLOCKED: EQY and Yosys absent | `build/formal.log` |
| EQY negative control and reachable SAT witness | BLOCKED: EQY and Yosys absent | `build/negative.log` |
| Yosys synthesis | BLOCKED: Yosys absent; gold/candidate/delta cell counts unavailable | `build/synth.log` |
| Full suite | Failed prerequisite gate; no `build/status` success marker | `build/all.log`, `build/all-inputs.txt` |
| Runner syntax | PASS: Bash and both Node modules parse | Commands below |

## Engineering decisions

| ID | Decision | Rationale |
| --- | --- | --- |
| D001 | Start with a single MAC and fixed arithmetic/reset contract | Establish a small reproducible baseline. |
| D002 | Keep a signedness mutant as a permanent negative control | Demonstrate observable signed-arithmetic sensitivity. |
| D003 | Introduce Rust execution before Go orchestration and Python proposals | Deferred to later phases per roadmap. |
| D004 | Use EQY SAT temporal induction with depth 10 and no external solver | Fits the small sequential arithmetic cone; requires completed proof, not merely bounded agreement. Compatibility and completion await real tools. |
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

1. Missing Verilator, Yosys, and EQY block every EDA exit criterion. Install a
   compatible toolchain, then run the full suite and replace pending results with
   observed versions, counts, proof status, and trace paths.
2. No usable Git metadata prevents recording a revision or validating from a
   clean Git checkout. Once a real checkout is available, record its commit plus
   input hashes and run the documented entry point there.

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
