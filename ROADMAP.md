# Roadmap

Complete each phase against its exit criteria. Keep the evidence from earlier phases so later changes can be evaluated against a known baseline.

## Phase 1 · Establish the local MAC baseline

Implement a gold MAC and a separate interface-compatible candidate. Add a deterministic Verilator scoreboard, a sequential EQY proof, a deliberately incorrect signed-arithmetic variant, and identical generic Yosys runs for gold and candidate.

**Exit criteria**

- The documented full-suite command succeeds from a clean checkout in the tested environment.
- Both good implementations pass the scoreboard, including reset, hold, signed extrema, and wraparound in both directions.
- EQY completes the post-reset proof for the good candidate and produces a reachable counterexample for the mutant.
- Both synthesis runs produce netlists and machine-readable counts. The development record captures commands, versions, outcomes, and artifact paths.

## Phase 2 · Build the Rust execution worker

Wrap the established simulation, equivalence, and synthesis commands in typed local jobs. Stage each attempt independently, supervise subprocesses, normalize outcomes, and emit a versioned evidence manifest. Reproduce the Phase 1 outcomes through the worker.

**Exit criteria**

- A real-tool integration run produces the expected results for both the good candidate and the mutant.
- Tests cover malformed reports, tool failure, timeout, cancellation, process-tree cleanup, and artifact isolation.
- Evidence can be traced back to exact inputs, tool versions, and an execution attempt.

## Phase 3 · Build the Go gRPC control plane

Expose job submission, status, and cancellation. Persist jobs and attempts and connect the Rust worker. Keep eligibility rules and retry policy in Go.

**Exit criteria**

- An end-to-end test submits a MAC candidate and retrieves its recorded evidence through gRPC.
- Tests cover idempotent submissions, lease expiry, stale completions, restart recovery, and cancellation.
- Jobs with missing evidence or non-passing verification stay ineligible. A verified candidate can be queued for human review.

## Phase 4 · Add the Python and LangGraph proposal loop

Build a bounded propose, validate, evaluate, and revise loop. Start with a deterministic fake proposer so the workflow can be tested independently of a model. Add a model adapter after the boundaries and budget behavior are demonstrated.

**Exit criteria**

- Tests demonstrate a valid candidate, a counterexample that informs a revision, a disallowed patch, and termination at the configured budget.
- The loop preserves evidence for each attempt and cannot modify the reference, proof assumptions, or scoring configuration.
- A reproducible run records the baseline, candidate lineage, and final eligibility.

## Phase 5 · Expand the accelerator and add physical evaluation

Extend the target to a small dot-product block or MAC array. Document numeric behavior, latency, and throughput. Add OpenROAD using pinned platform inputs and a fixed comparison flow.

**Exit criteria**

- The larger block has a trusted reference, simulation coverage, and a documented equivalence strategy.
- At least one candidate and its baseline are evaluated with the same physical-design settings. Report timing, area, throughput where relevant, and implementation stage. Record the activity assumptions for any power estimate.
- The final evaluation documents verified improvements, regressions, and inconclusive attempts alongside the settings and artifacts needed to reproduce them.