# Local worker staging

`worker/` starts Phase 2. It stages inputs only; it does not execute RTL, report
verification outcomes, or decide eligibility.

```sh
cargo test --manifest-path worker/Cargo.toml
cargo clippy --manifest-path worker/Cargo.toml --all-targets -- -D warnings
mkdir -p build/attempts
cargo run --manifest-path worker/Cargo.toml -- \
  . build/attempts local-job attempt-1 rtl/candidate/mac_candidate.sv
```

The five positional arguments are the trusted repository root, existing attempt
parent directory, logical job ID, execution attempt ID, and replacement source.
IDs contain 1–64 ASCII letters, digits, underscores, or hyphens. Each attempt
uses a length-prefixed job identity followed by the attempt identity to avoid
ambiguous directory names. Existing attempt directories are never reused.

The library accepts a full-file replacement only for
`rtl/candidate/mac_candidate.sv`. The source must be nonempty UTF-8, at most
1 MiB, without NUL bytes or backticks. Rejecting every backtick intentionally
forbids all preprocessing, including includes and macro expansion, even in
comments. This conservative subset can be widened after isolated execution is
implemented. It does not establish that RTL is syntactically valid or safe.

The trusted root and attempt parent must be controlled by the operator, stable
during staging, and inaccessible to proposers. This API does not defend against
concurrent privileged filesystem mutation. Trusted relative paths are fixed in
the worker; symlinks in those paths are rejected. Only allowlisted files are
copied, each bounded to 1 MiB. Credentials, Git metadata, tools, and previous
artifacts are excluded. Staged files have read-only permissions, which do not
provide isolation against a process running as their owner.

`PREPARED` is written last. A staging I/O failure can leave an incomplete
directory without this marker; retain it for diagnosis and use a new attempt
identity. The marker is not an evidence manifest or a verification result.

Remaining Phase 2 work includes an enforced sandbox with bounded writable
storage and process resources, process-tree supervision, timeout/cancellation,
strict report parsing, source/configuration hashes and tool versions, atomic
evidence publication, and real-tool good/mutant integration runs. The Phase 1
shell runner remains a trusted fixture and must not execute proposals directly.
