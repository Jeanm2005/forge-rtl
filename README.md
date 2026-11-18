# Forge RTL

Forge RTL evaluates agent-proposed SystemVerilog changes for TinyML accelerators.
It checks each candidate against a reviewed reference, measures it with a pinned
synthesis flow, and records the evidence needed for human review. The current
target is a signed INT8 multiply-accumulate unit with wrapping INT32 arithmetic.

## How it works

1. **Propose.** A bounded LangGraph loop asks a scripted proposer or a local
   Ollama model for a replacement of the candidate module. A parent hash binds
   each proposal to the exact source it revises.
2. **Evaluate.** The Go scheduler sends the job to an isolated Rust worker.
   Verilator runs an independent scoreboard, EQY checks sequential equivalence
   after reset, and Yosys measures generic cell counts under identical settings.
3. **Review.** The control plane validates the evidence before granting
   eligibility. Failed proofs can feed a revision. An operator can queue a
   passing candidate for human review. Promotion requires human approval.

The reproducible demo follows a passing baseline, a zero-extension mutant with a
reachable counterexample, and a passing sign-extension revision. The baseline
and revision each use **988 generic cells**. These counts measure synthesized
structure. Physical area, timing, and power require a physical-design flow.

## Quick start

Use a Linux x86-64 host with a working user systemd session, Bubblewrap, Rust,
Go 1.26.11, and Python 3.11 or newer. CI uses Python 3.14.4.
[Worker setup and sandbox requirements](docs/worker.md)

Run the RTL baseline from the repository root. The check recreates `build/`,
so archive any existing runs first.

```bash
make setup
source .tools/env.sh
make check
```

Build and test the worker and control plane, then run the proposal demo through
gRPC with a scripted proposer.

```bash
make control-check
python3 -m venv .tools/proposal-venv
source .tools/proposal-venv/bin/activate
python -m pip install -r proposal/requirements-lock.txt
make proposal-check
bash scripts/proposal-integration.sh
```

The demo prints the path to `build/proposal.*/run.zip`. Its versioned index
links each candidate to its parent and to the recorded job and manifest. The
adjacent state directory contains worker logs, netlists, and counterexample
traces. GitHub Actions also runs the demo from a clean checkout and uploads the
archive and worker evidence. [Verification runs](https://github.com/Jeanm2005/forge-rtl/actions/workflows/phase3.yml)

The proposal guide covers model-backed runs. The control-plane guide covers
manual submissions and human review. [Proposal guide](docs/proposal-loop.md) ·
[Control-plane guide](docs/control-plane.md)

## Design decisions

| Layer | Why it owns this work |
| --- | --- |
| **SystemVerilog and EDA tools** | SystemVerilog expresses the MAC’s signed arithmetic, reset priority, and enable behavior in the same design used for synthesis. An independent scoreboard and formal equivalence catch different classes of errors. A permanent signedness mutant checks that the verification flow detects a known defect. |
| **Rust worker** | Typed input validation and memory-safe artifact parsing fit the process that handles untrusted source. Bubblewrap and user systemd enforce resource and process-tree limits, which makes Linux sandbox support a deployment requirement. |
| **Go and gRPC** | A typed service owns deadlines, cancellation, retry budgets, and eligibility. Versioned RPCs keep the worker and proposal loop independent. A Unix socket and an atomic local state file simplify deployment and recovery while limiting scheduling to one host. |
| **Python and LangGraph** | Python fits the model integration ecosystem. LangGraph makes proposal and revision steps explicit and testable with a scripted proposer. Models receive the current candidate and structured feedback. Trusted verification inputs and review decisions stay outside the proposal loop. |
