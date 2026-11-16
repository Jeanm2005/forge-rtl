# Proposal loop

Phase 4 provides an immutable Python proposal contract and a bounded LangGraph
workflow with a deterministic scripted proposer. The gRPC adapter, structured
evidence interpretation, durable run export, and model adapter remain subsequent
subtasks. This package does not execute EDA tools or decide candidate eligibility.

Proposers return UTF-8 JSON with exactly these fields:

```json
{
  "schema_version": 1,
  "path": "rtl/candidate/mac_candidate.sv",
  "parent_sha256": "<SHA-256 of the exact parent bytes>",
  "source": "<complete replacement SystemVerilog source>"
}
```

The file replacement is the patch format; shell commands and unified diffs are
not accepted. The parent hash binds each proposal to its lineage. Source bytes
are immutable and hashed without newline normalization. The parser accepts only
the exact allowlisted path, rejects duplicate and extra JSON fields, and bounds
source size to the worker's 1 MiB limit. Preprocessing and NUL bytes are rejected
before submission. Rust remains authoritative for the complete restricted RTL
grammar, staging, and execution. Python acceptance does not imply valid RTL or
eligibility. The Go control plane remains responsible for lifecycle and policy.

`load_baseline` reads from a trusted, quiescent checkout and rejects symlinks in
the candidate path. The checkout root itself is trusted. This helper is not a
concurrent untrusted-filesystem sandbox. Applying replacements creates in-memory
candidates and never modifies repository files.

Run the Python checks from the repository root:

```sh
python3 -m venv /tmp/forge-proposal-venv
/tmp/forge-proposal-venv/bin/pip install -r proposal/requirements-lock.txt
PATH=/tmp/forge-proposal-venv/bin:$PATH make proposal-check
```

The checks run Ruff formatting/lint, strict mypy, and unit tests. Python 3.11+
is required; the initial implementation was checked with Python 3.14.4.
`requirements-lock.txt` records the complete tested runtime and development
environment; `requirements-runtime.txt` and `requirements-dev.txt` declare direct
dependencies for intentional updates.

## Workflow and budgets

`run_loop(baseline, proposer, evaluator, budget, run_id)` compiles a LangGraph
`StateGraph` with propose, validate, evaluate, and budget-decision nodes. It first
evaluates the baseline. Baseline eligibility supplies comparison context; only
an eligible proposed candidate ends the run successfully. The graph follows the
[LangGraph conditional-edge API](https://reference.langchain.com/python/langgraph/graph/state/StateGraph/add_conditional_edges).

`Budget` allows 0..100 proposal calls and 1..101 evaluation calls. The baseline
consumes one evaluation. Invalid proposals consume a proposal slot without an
evaluation. Every branch returns to a budget check before another proposal;
evaluation exhaustion takes precedence when both budgets are exhausted. An
eligible result takes precedence over budget exhaustion. The graph recursion
limit is derived from the budget and is a secondary guard.

Valid replacements become the next parent after evaluation, including a
counterexample. Validation rejection retains the previous parent. Both recorded
evaluation feedback and rejection diagnostics reach the next proposer call.
`FakeProposer` takes a tuple of `Step` objects with replacement source, path, and
an expected feedback substring. A script mismatch raises an error, making tests
demonstrate that counterexample feedback actually reaches a revision.

The injected `Evaluator` owns the eventual control-plane connection. It receives
the candidate and a stable submission key (`run_id` plus evaluation sequence).
Reuse the run ID only to replay the same inputs, policy, budgets, and deterministic
proposal sequence. New runs need distinct IDs. This subtask does not implement
restart/resume or network retries. Each evaluator call must enforce its own
deadline; graph budgets count calls and do not interrupt a blocking adapter or
proposer. Infrastructure retry policy belongs to Go.

`Evaluation` carries the candidate hash, logical job ID, distinct outcome,
explicit eligibility, exact manifest bytes and their hash, and bounded feedback.
The future adapter must validate retrieved evidence and populate these fields
from Go's recorded result. The graph rejects candidate-hash mismatches and stops
on `EvaluationError`, retaining preceding trials and the failed candidate. Other
programming errors propagate to the caller. It never infers eligibility from a
tool log or initiates promotion or review.

The returned immutable `Run` retains baseline bytes, every raw proposal,
parent/child lineage, validation rejections, and each evaluation's exact manifest.
This is in-memory history, not crash-durable storage. Adapter-backed durable run
export is a later subtask. Current workflow tests use synthetic evaluator fixtures;
they make no real-tool, physical improvement, or end-to-end eligibility claim.
