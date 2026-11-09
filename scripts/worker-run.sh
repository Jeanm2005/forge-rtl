#!/usr/bin/env bash
set -Eeuo pipefail
# stdout is reserved for the bounded artifact transport.
exec 3>&1
exec 1>&2
mkdir -p build/worker
run() {
    local name=$1 start rc=0
    shift
    start=$(python3 -c 'import time; print(time.monotonic_ns() // 1000000)')
    "$@" > "build/worker/$name.log" 2>&1 || rc=$?
    printf '%s\n' "$rc" > "build/worker/$name.exit"
    printf '%s\n' "$(( $(python3 -c 'import time; print(time.monotonic_ns() // 1000000)') - start ))" > "build/worker/$name.duration_ms"
}
run tools bash scripts/check.sh tools
if [[ $(cat build/worker/tools.exit) == 0 ]]; then
    run simulation bash scripts/check.sh sim
    run equivalence bash scripts/check.sh formal
    if [[ -f build/formal/PASS && ! -e build/formal/FAIL ]]; then
        run equivalence_audit node scripts/audit-eqy.mjs build/formal
    else
        run equivalence_audit node scripts/audit-eqy.mjs build/formal negative
    fi
    mkdir -p build/reachable
    cat > build/reachable/flow.ys <<'YS'
read_verilog -sv rtl/gold/mac_gold.sv rtl/candidate/mac_candidate.sv formal/candidate-reachability.sv
prep -top reachability -flatten
async2sync
dffunmap
sat -seq 3 -set-init-undef -set-def-inputs -prove ok 1 -show-inputs -show-outputs -show first -dump_vcd build/reachable/counterexample.vcd -dump_json build/reachable/counterexample.json
YS
    run reachable timeout 180s yosys -l build/reachable/yosys.log build/reachable/flow.ys
    run synthesis bash scripts/check.sh synth
fi
exec tar --format=ustar --exclude='./sim/obj' -cf /proc/self/fd/3 -C build .
