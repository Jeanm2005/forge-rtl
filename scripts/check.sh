#!/usr/bin/env bash
set -Eeuo pipefail
cd "$(dirname "$0")/.."
mode=${1:-all}
case "$mode" in all|tools|sim|formal|negative|synth) ;; *) exit 2 ;; esac
mkdir -p build
rm -f build/status
if [[ $mode == all ]]; then
    rm -rf build
    mkdir build
fi
exec > >(tee "build/${mode}.log") 2>&1
trap 'rc=$?; echo "FAIL stage=$mode exit=$rc"; exit "$rc"' ERR
snapshot() {
    date -u +%FT%TZ
    uname -a
    cat /etc/os-release
    if [[ -f .tools/oss-cad-suite/.forge-toolchain-sha256 ]]; then
        cat .tools/oss-cad-suite/.forge-toolchain-sha256
    fi
    git rev-parse HEAD 2>/dev/null || echo 'revision unavailable: no usable Git metadata'
    find rtl tests formal scripts -type f -print0 | sort -z | xargs -0 sha256sum
    sha256sum Makefile
}
snapshot > "build/${mode}-inputs.txt"
require() {
    local missing=0
    for tool in "$@"; do
        if ! command -v "$tool"; then echo "BLOCKED: missing prerequisite $tool"; missing=1; fi
    done
    [[ $missing == 0 ]]
}
versions() {
    verilator --version
    yosys -V
    eqy --version
    g++ --version | head -1
    make --version | head -1
    node --version
}
tools() {
    require verilator yosys eqy g++ make node timeout
    versions > build/versions.txt
    cat build/versions.txt
}
sim() {
    rm -rf build/sim
    mkdir build/sim
    require verilator g++ make timeout
    timeout 180s verilator --binary --timing --timescale 1ns/1ps --top-module tb_mac -Wall \
        --Mdir build/sim/obj rtl/gold/mac_gold.sv rtl/candidate/mac_candidate.sv \
        tests/tb_mac.sv > build/sim/compile.log 2>&1
    timeout 60s build/sim/obj/Vtb_mac | tee build/sim/result.log
    grep -q '^PASS cycles=' build/sim/result.log
}
formal() {
    rm -rf build/formal
    require eqy yosys node timeout
    timeout 180s eqy -f -d build/formal formal/good.eqy
    test -f build/formal/PASS
    test ! -e build/formal/FAIL
    node scripts/audit-eqy.mjs build/formal
    # Preserve match and partition metadata alongside all generated strategy logs.
    test -d build/formal/partitions
    find build/formal/partitions -type f -print > build/formal/partition-files.txt
    test -s build/formal/partition-files.txt
    grep -R -q 'acc' build/formal/partitions
}
negative() {
    rm -rf build/negative
    mkdir build/negative
    require eqy yosys node timeout
    sed -e 's@rtl/candidate/mac_candidate.sv@tests/mac_mutant.sv@' \
        -e 's/DUT=mac_candidate/DUT=mac_mutant/' formal/good.eqy > build/negative/mutant.eqy
    local rc=0
    timeout 180s eqy -f -d build/negative/eqy build/negative/mutant.eqy || rc=$?
    [[ $rc == 2 ]]
    test -f build/negative/eqy/FAIL
    test ! -e build/negative/eqy/PASS
    node scripts/audit-eqy.mjs build/negative/eqy negative
    if grep -R -E '^(ERROR|TIMEOUT)' build/negative/eqy/strategies --include=status; then
        echo 'Negative control encountered a strategy error or timeout'
        return 1
    fi
    if grep -E 'ERROR:|Traceback \(most recent call last\)' build/negative/eqy/logfile.txt; then
        return 1
    fi
    # An EQY failure alone is insufficient: independently demand a reachable SAT witness.
    cat > build/negative/reachable.ys <<'YS'
read_verilog -sv rtl/gold/mac_gold.sv tests/mac_mutant.sv formal/reachability.sv
prep -top reachability -flatten
async2sync
dffunmap
sat -seq 3 -set-init-undef -set-def-inputs -prove ok 1 -show-inputs -show-outputs -show first -dump_vcd build/negative/counterexample.vcd -dump_json build/negative/counterexample.json
YS
    timeout 180s yosys -l build/negative/reachable.log build/negative/reachable.ys
    grep -q 'SAT proof finished - model found: FAIL!' build/negative/reachable.log
    test -s build/negative/counterexample.vcd
    test -s build/negative/counterexample.json
    echo 'PASS negative control: reachable post-reset counterexample (depth 3)'
}
synth() {
    rm -rf build/synth
    mkdir -p build/synth/gold build/synth/candidate
    require yosys node timeout
    for variant in gold candidate; do
        local top="mac_$variant"
        cat > "build/synth/$variant/flow.ys" <<YS
read_verilog -sv rtl/$variant/$top.sv
hierarchy -check -top $top
synth -top $top
check -assert
tee -o build/synth/$variant/stat.json stat -json
write_verilog -noattr build/synth/$variant/netlist.v
write_json build/synth/$variant/netlist.json
YS
        timeout 180s yosys -l "build/synth/$variant/yosys.log" "build/synth/$variant/flow.ys"
    done
    node scripts/counts.mjs | tee build/synth/comparison.json
}
if [[ $mode == all ]]; then
    tools
    sim
    formal
    negative
    synth
    echo PASS > build/status
else
    "$mode"
fi
