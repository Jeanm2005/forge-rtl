#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
output="$(mktemp -d "$PWD/build/proposal.XXXXXX")"
state="$output/state"

build/forge-control -state "$state" > "$output/daemon.log" 2>&1 &
daemon=$!
trap 'kill "$daemon" 2>/dev/null || true; wait "$daemon" || true' EXIT

for ((i=0; i<100; i++)); do
    [[ ! -S "$state/control.sock" ]] || break
    if ! kill -0 "$daemon" 2>/dev/null; then
        cat "$output/daemon.log" >&2
        exit 1
    fi
    sleep 0.1
done
if [[ ! -S "$state/control.sock" ]]; then
    cat "$output/daemon.log" >&2
    exit 1
fi
PYTHONPATH="$PWD/proposal" python -m forge_proposal.demo \
    --socket "$state/control.sock" --run-id mac-sign-extension \
    --output "$output/run.zip" | tee "$output/demo.log"
kill "$daemon"
wait "$daemon"
trap - EXIT
