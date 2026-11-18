#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

out=$(realpath -m "${1:-build/dot4}")
mkdir -p "$out"
verilator --version > "$out/tools.txt"
eqy --version >> "$out/tools.txt"
yosys -V >> "$out/tools.txt"
sha256sum rtl/{gold,candidate}/dot4_*.sv formal/dot4/dot4.eqy tests/tb_dot4.sv \
    > "$out/verification-inputs.sha256"
verilator --binary --timing --assert -Wall --top-module tb_dot4 \
    --Mdir "$out/obj" tests/tb_dot4.sv rtl/{gold,candidate}/dot4_*.sv \
    > "$out/verilator.log" 2>&1
"$out/obj/Vtb_dot4" | tee "$out/simulation.log"
eqy -f -d "$out/equivalence" formal/dot4/dot4.eqy | tee "$out/equivalence.log"
