#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

root=$PWD
platform=$root/build/physical-platform
out=$(realpath -m "${1:-build/dot4-physical}")
mkdir -p "$out"
bash scripts/dot4-check.sh "$out/verification"

export DOT4_LIBERTY=$platform/NangateOpenCellLibrary_typical.lib
export DOT4_TECH_LEF=$platform/NangateOpenCellLibrary.tech.lef
export DOT4_CELL_LEF=$platform/NangateOpenCellLibrary.macro.mod.lef
export DOT4_SDC=$root/physical/dot4/constraints.sdc

(cd "$platform" && sha256sum -c "$root/physical/dot4/platform.sha256")
openroad -version > "$out/physical-tools.txt"
yosys -V >> "$out/physical-tools.txt"
sha256sum physical/dot4/{constraints.sdc,place.tcl,platform.sha256} \
    scripts/{dot4-physical.sh,setup-dot4-physical.sh} > "$out/flow-inputs.sha256"
cp physical/dot4/platform.sha256 "$out/platform.sha256"
cp "$DOT4_SDC" "$out/constraints.sdc"

for variant in gold candidate; do
    mkdir -p "$out/$variant"
    cp "rtl/$variant/dot4_$variant.sv" "$out/$variant/design.sv"
    (
        cd "$out/$variant"
        cat > map.ys <<YOSYS
read_liberty -lib "$DOT4_LIBERTY"
read_verilog -sv design.sv
rename dot4_$variant dot4
synth -top dot4 -noabc
dfflibmap -liberty "$DOT4_LIBERTY"
abc -liberty "$DOT4_LIBERTY"
clean
check -assert -mapped
stat -liberty "$DOT4_LIBERTY"
write_verilog -noattr -noexpr mapped.v
YOSYS
        timeout 180 yosys -Q -T -s map.ys > synthesis.log 2>&1
        timeout 180 openroad -no_init -exit "$root/physical/dot4/place.tcl" > placement.log 2>&1
        test -s placed.odb
        test -s metrics.tsv
        test -s setup.rpt
        test -s hold.rpt
    )
done

printf 'variant\tstage\tcell_area_um2\tsetup_wns_ns\thold_wns_ns\n' > "$out/comparison.tsv"
for variant in gold candidate; do
    {
        printf '%s\t' "$variant"
        cut -f 2 "$out/$variant/metrics.tsv" | paste -sd '\t'
    } >> "$out/comparison.tsv"
done
cat "$out/comparison.tsv"
