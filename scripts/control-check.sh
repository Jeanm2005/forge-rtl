#!/usr/bin/env bash
set -Eeuo pipefail
cd "$(dirname "$0")/.."
mkdir -p build
rm -f build/control-status
exec > >(tee build/control-check.log) 2>&1
go version
rustc --version
cargo fmt --manifest-path worker/Cargo.toml -- --check
cargo clippy --manifest-path worker/Cargo.toml --locked --all-targets -- -D warnings
cargo build --manifest-path worker/Cargo.toml --locked
if [[ -n $(gofmt -l controlplane) ]]; then
    echo 'Go formatting differs; run gofmt -w controlplane' >&2
    exit 1
fi
bash -n scripts/control-check.sh scripts/worker-check.sh scripts/worker-run.sh
go -C controlplane vet ./...
go -C controlplane build -race -o ../build/forge-control ./cmd/forge-control
go -C controlplane build -o ../build/forge-client ./cmd/forge-client
FORGE_REAL_TOOLS=1 go -C controlplane test -race -count=1 -v ./...
cargo test --manifest-path worker/Cargo.toml --locked -- --include-ignored --test-threads=1
printf 'PASS\n' > build/control-status
