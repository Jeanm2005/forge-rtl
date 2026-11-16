# Proposal loop

Phase 4 begins with an immutable Python proposal contract. The graph, gRPC
adapter, evidence interpretation, budgets, and model adapter remain subsequent
subtasks. This package does not execute tools or decide candidate eligibility.

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
/tmp/forge-proposal-venv/bin/pip install -r proposal/requirements-dev.txt
PATH=/tmp/forge-proposal-venv/bin:$PATH make proposal-check
```

The checks run Ruff formatting/lint, strict mypy, and unit tests. Python 3.11+
is required; the initial implementation was checked with Python 3.14.4.
