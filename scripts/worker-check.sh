#!/usr/bin/env bash
set -Eeuo pipefail
cd "$(dirname "$0")/.."
mkdir -p build
rm -f build/worker-status
exec > >(tee build/worker-check.log) 2>&1
rustc --version
cargo --version
bwrap --version
systemctl --version | head -1
cargo fmt --manifest-path worker/Cargo.toml -- --check
cargo clippy --manifest-path worker/Cargo.toml --locked --all-targets -- -D warnings
bash -n scripts/worker-run.sh scripts/worker-check.sh
cargo test --manifest-path worker/Cargo.toml --locked -- --include-ignored --test-threads=1 --nocapture
printf 'PASS\n' > build/worker-status
