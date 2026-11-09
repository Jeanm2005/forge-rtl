# AGENTS.md

## Objective

Build an agent-driven RTL optimization system for TinyML accelerators. An agent proposes a SystemVerilog patch. A trusted evaluation flow checks the candidate against a reviewed reference and measures it under a reproducible synthesis or physical-design flow. A human reviews eligible candidates before promotion.

## Architecture and ownership

| Layer | Owns |
| --- | --- |
| SystemVerilog and verification | Golden RTL, candidate RTL, interface and arithmetic contracts, simulation scoreboards, and formal harnesses. Verilator runs simulation. EQY checks equivalence. SymbiYosys supports proof strategies and standalone property or cover jobs. Yosys runs synthesis. OpenROAD enters during physical evaluation. |
| Rust EDA workers | Typed job input validation, source staging, process supervision, resource enforcement, result parsing, and evidence manifests. |
| Go gRPC control plane | Submission, scheduling, job and attempt identity, leases, deadlines, retries, cancellation, durable state, and eligibility decisions. |
| Python and LangGraph | Proposal generation, structured evidence interpretation, and bounded revision. The graph submits candidates through the control plane and consumes recorded outcomes. |

Keep the boundaries visible in code. Python proposes changes. Rust executes trusted job definitions and records what happened. Go owns lifecycle and policy. The verification tools produce the evidence that supports a correctness decision.

Version the RPC and evidence contracts when they have real callers. Distinguish a logical job from an execution attempt. Make submissions idempotent, reject completions from expired attempts, and publish artifacts before committing a terminal result. Propagate cancellation to the worker and its subprocess tree. Retry transient infrastructure failures within a defined budget.

## Trust and evaluation

- Treat proposed patches, candidate RTL, logs, and traces as untrusted input. Limit patches to allowlisted candidate RTL paths. Reject traversal, symlinks, and includes that escape staged inputs.
- Protect the golden reference, tests, assertions, proof assumptions, tool scripts, and scoring configuration from candidate edits. Review changes to these inputs separately. Document proof assumptions and demonstrate that key behaviors remain reachable.
- Before executing agent-generated RTL, isolate each attempt with read-only staged inputs, bounded writable storage, no credentials, disabled network access, and limits on time, CPU, memory, subprocesses, and output. Invoke tools through trusted command templates and explicit argument lists.
- Distinguish a completed proof, a counterexample, an inconclusive result, a timeout, a cancellation, and an infrastructure error. Record bounded checks with their depth. Require explicit success from each applicable gate before a candidate becomes eligible.
- Record source and configuration hashes, tool and solver versions, seeds where applicable, resource limits, outcome, duration, and artifact locations. Preserve counterexample traces when available.
- Use the same flow settings for candidate comparisons. Report generic Yosys cell counts as structural metrics. For physical results, record the implementation stage, platform, library, corner, and constraints. Require human review before promotion.

## Working instructions

1. Read `ROADMAP.md` and the current entry in `DEVELOPMENT.md`. Inspect existing entry points and preserve user changes.
2. Work on the requested phase. Keep changes reviewable and introduce service or framework code when the phase calls for it.
3. Use the simplest implementation that preserves the contract. Add code comments only when requested or necessary to explain an invariant, tool workaround, or required directive. Put interface contracts and usage in documentation.
4. In SystemVerilog, use explicit widths and signedness, sized constants, and nonblocking assignments for sequential state. Define reset, enable, latency, and overflow. Separate synthesis RTL from simulation and formal harnesses.
5. Apply `gofmt` for Go, `cargo fmt` and Clippy for Rust, and the repository’s formatter, linter, and type checks for Python as each layer arrives. Prefer typed boundaries and structured errors.
6. Keep generated artifacts in ignored build or run directories. Run the relevant checks and report the exact commands, observed outcomes, evidence paths, and any blocked checks. Update `DEVELOPMENT.md` when a run changes the project’s status, establishes a baseline, or changes an engineering decision.
7. Keep Jean Cruz <jmarcoc.2005@gmail.com> as the sole author and committer of requested commits. Do not add coauthor trailers. Commit each requested subphase separately and push when authorized by the user.
