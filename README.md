# TinyML RTL Optimization

Phase 1 provides a local signed INT8 MAC evaluation fixture. The implementation
is present, but validation is **blocked by missing EDA tools** in the supplied
environment. No optimization or completed baseline is claimed. Later phases
(Rust, Go, gRPC, proposal agents, LangGraph, model calls, and OpenROAD) are deferred.

## Arithmetic and timing contract

Both `mac_gold` and `mac_candidate` expose `clk`, active-high synchronous `rst`,
`en`, signed 8-bit `a` and `b`, and signed 32-bit `acc`. At each rising edge:

- `rst` clears `acc` and takes priority over `en`.
- Otherwise, `en` adds the exact signed 16-bit product, sign-extended to 32 bits,
  modulo 2^32.
- Otherwise, `acc` holds its value.

The updated result is observable after sequential updates settle. State before
the first reset is unspecified. Gold uses direct widened arithmetic; candidate
spells out the product, extension, and next-state sum. Candidate is an equivalence
fixture until measurements justify an optimization claim.

## Prerequisites and environment

The inspected environment is **Ubuntu 26.04.1 LTS on WSL2**, x86-64,
kernel `6.18.54.1-microsoft-standard-WSL2`. Available: GNU Make 4.4.1,
G++ 15.2.0, Node.js v24.22.1, Bash and GNU coreutils. Node parses evidence only;
there is no proposal service or agent framework.

Required on `PATH`: `verilator`, `yosys`, `eqy`, `sby`, `bitwuzla`, `g++`, `make`, `node`, `timeout`,
plus the usual shell/coreutils tools. Use a compatible Yosys/EQY installation
(e.g. a complete OSS CAD Suite), rather than mixing unverified releases.
Verilator must support `--binary --timing`; Node must support ES modules.
The installer supports Linux x86-64, including Ubuntu on WSL2. It installs
missing system packages through apt (using sudo when needed), requires Node 18+,
and installs OSS CAD Suite **2026-11-05** under ignored `.tools/`. The archive
SHA-256 is pinned in `scripts/toolchain.env` and checked before extraction. No
system EDA installation is overwritten. The archive is approximately 749 MB,
and extraction requires additional disk space. Run `source .tools/env.sh` in
each new shell. The setup command needs internet access.

EQY uses SBY with `smtbmc bitwuzla` and temporal induction, depth 10.
SBY and Bitwuzla come from the same pinned suite. The independent depth-3
counterexample check uses Yosys's built-in SAT solver.

Validated on Ubuntu 24.04 GitHub Actions: Verilator 5.055
(`v5.054-63-g57bbb293d`), Yosys 0.70+336 (`8f3cc3d0c`),
EQY v0.70-2-g3301516, SBY v0.70-9-g79fc9ed, and Bitwuzla 0.9.1.
Phase 1 passes: 273,187 simulation cycles, completed equivalence proof,
reachable mutant counterexample, and 988 generic cells for each implementation.
See [DEVELOPMENT.md](DEVELOPMENT.md) for commands and evidence.

## Commands

Run from the repository root:

```sh
make setup       # installs system prerequisites if needed; downloads pinned EDA tools
source .tools/env.sh
make clean
make check       # full suite: prerequisites, simulation, proof, mutant, synthesis
```

Individual checks:

```sh
make tools
make sim
make formal
make negative
make synth
```

Each stage removes its own previous output before checking prerequisites. The
full suite clears all `build/` evidence first and stops at the first failed gate.
Only a completed full suite writes `build/status` containing `PASS`.
Tool execution has wall limits: simulation compilation 180 s, simulation 60 s,
EQY 180 s per design, bounded SAT 180 s, synthesis 180 s per design. These are
local trusted fixtures; this runner does not provide the isolation required for
future agent-generated RTL. Do not use it as a sandbox for untrusted submissions.

## Verification and evidence

All generated output lives in ignored `build/`. Input SHA-256 hashes and
available revision identity are saved in `build/*-inputs.txt`; stage console
logs are `build/{all,sim,formal,negative,synth,tools}.log` as applicable.

| Check | Method and retained artifacts |
| --- | --- |
| Simulation | `build/sim/compile.log`, `result.log`, generated Verilator executable. Both implementations checked each cycle against independent signed 64-bit arithmetic truncated to 32 bits. Inputs change while clock is low; output sampled 1 ns after rising edge. |
| Good proof | `build/formal/`: EQY `PASS`, matched IDs, partition list, generated RTLIL, strategy logs/status. `scripts/audit-eqy.mjs` rejects internal cut points, requires matched public `acc`, and requires every generated partition's induction strategy to pass. |
| Negative control | `build/negative/eqy/` must report failure; an independent depth-3 Yosys SAT run must confirm a reachable failure and write `counterexample.vcd`, `counterexample.json`, and `reachable.log`. Timeout/error alone cannot pass. |
| Synthesis | `build/synth/{gold,candidate}/`: `flow.ys`, `yosys.log`, `netlist.v`, `netlist.json`, `stat.json`. `comparison.json` records gold/candidate totals and per-type counts, and candidate-minus-gold deltas. The parser cross-checks statistics against netlists and verifies selected top. |

The scoreboard's fixed xorshift32 seed is `0x1badf00d`. Directed sequences cover
reset priority, reset after accumulated work, bubbles, consecutive updates,
zero, both signed extrema, and mixed signs. Repeating (-128)×(-128) 131073 times
crosses the positive boundary; repeating (-128)×127 132105 times after reset
crosses the negative boundary. Coverage counters must observe both wrap directions.
There are 10000 additional deterministic stimulus cycles. A mismatch reports
cycle, reset, enable, operands, expected value, and both actual outputs.

The trusted formal wrapper initializes a one-cycle prefix flag, forces reset on
the first transition, and masks the observation only before that transition.
Subsequent reset, enable, and operands are unconstrained. No assumption fixes
later operands or suppresses arithmetic. Internal state/product matches are
excluded so the output comparison includes the complete sequential cone. The
negative-control miter uses the same reset prefix; since its output is true
before reset, a depth-3 mismatch necessarily occurs after reset and legal inputs.
Its mutant zero-extends the exact signed product. Bounded SAT is counterexample
evidence only; the good check requires a completed induction proof.

Both synthesis runs use `hierarchy -check`, `synth -top`, `check -assert`, and
`stat -json`, without a liberty library or physical constraints. Counts describe
generic Yosys cells, not area, timing, or power. Compare only identical tool
versions and settings. Human review remains required before promotion.

The proof setup follows the official EQY documentation for
[strategies](https://yosyshq.readthedocs.io/projects/eqy/en/latest/strategies.html)
and [output evidence](https://yosyshq.readthedocs.io/projects/eqy/en/latest/outdir.html).

## CI and delivery

[`.github/workflows/phase1.yml`](.github/workflows/phase1.yml) runs on pushes,
pull requests, and manual dispatch, using Ubuntu 24.04 on GitHub-hosted runners.
It pins third-party actions to commit SHAs, installs Node 24.22.1, runs the same
checksum-verified setup script as local development, checks runner syntax, and
executes `make check`. The job has read-only repository permissions and does
not persist checkout credentials.

Successful runs deliver `phase1-verified-<commit>` containing a compressed
evidence bundle and its SHA-256 digest, retained for 30 days. The bundle includes
source hashes, tool versions, revision identity, simulation/proof logs, mutant
traces, and synthesis netlists/counts. Failure diagnostics are retained for 14
days. GitHub's Actions tab provides artifact downloads. No secret is required,
and no deployment service or automatic candidate promotion is introduced.
Human review is still required before promotion.

The workflow is implemented but has **not run remotely**: this session cannot
push to GitHub. Local workflow validation with `actionlint` passed; EDA setup
stopped at DNS resolution, so Ubuntu 24.04 is the configured CI platform rather
than an observed passing test environment.

## Project documents

- [AGENTS.md](AGENTS.md): ownership and implementation rules.
- [ROADMAP.md](ROADMAP.md): phase scope and exit criteria.
- [DEVELOPMENT.md](DEVELOPMENT.md): observed results and unresolved checks.
