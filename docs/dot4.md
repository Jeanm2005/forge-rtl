# Four-lane signed dot product

Each lane is a two's-complement INT8 in bits `8*i +: 8` of each packed
operand. The block multiplies matching lanes, sums the four signed 16-bit
products, and returns the exact signed 18-bit result. The range is −65,024
through 65,536. The block has no accumulation across cycles, saturation,
rounding, or overflow on valid operands.

The rising edge samples both operands when `en` is high and publishes their
dot product from the output register after that edge. The latency is one
register stage and the throughput is one vector per enabled cycle (four
products per cycle). A low enable holds the previous result. Synchronous
active-high reset takes priority over enable and clears the register. Apply
reset before observing the output.

The baseline adds products left to right. The carry-save candidate
sign-extends each product to 18 bits, reduces the four terms to two with
two 3-to-2 compressor stages, and uses a single carry-propagating adder.
The compressors preserve the sum modulo 2¹⁸, which represents the exact
result throughout the valid signed range.

## Verification

Run `source .tools/env.sh` and `make dot4-check`. The Verilator scoreboard
uses independent 32-bit signed arithmetic and covers extrema, mixed signs,
reset priority, output hold, and back-to-back updates across 4,103 cycles.

EQY proves each of the four product cutpoints separately, then proves the
registered output with those matched product values as free inputs. The output
proof therefore covers even values outside the range of an INT8 product.
The ABC PDR strategy must report a completed proof. Enable, reset, and
operands are unconstrained, and the interface contract starts at the first
reset edge. Logs, partition definitions, and input hashes are in `build/dot4/`.

## Placement comparison

On Ubuntu 22.04 x86-64, run the following from the repository root.

```sh
make setup
source .tools/env.sh
bash scripts/setup-dot4-physical.sh
sudo apt-get update
sudo apt-get install -y --no-install-recommends "$PWD/build/physical-platform/openroad.deb"
make dot4-physical
```

The [OpenROAD package](https://github.com/Precision-Innovations/OpenROAD/releases/tag/2024-12-14)
and the [Nangate45 platform inputs](https://github.com/The-OpenROAD-Project/OpenROAD-flow-scripts/tree/5cbd5178dc15264dbb2c6233922953347b6de92c/flow/platforms/nangate45)
are pinned by SHA-256. The run first verifies both implementations. Yosys
maps both to NangateOpenCellLibrary typical (1.10 V, 25 °C), with the same
synthesis and ABC options. OpenROAD uses one thread, an 80 × 80 µm die,
a core from (3.04, 4.20) to (77, 77) µm, a target placement density of 0.55,
metal3 horizontal and metal2 vertical pin layers, and metal3
placement-estimated signal RC. Both designs use a 3 ns ideal clock, 50 ps uncertainty, input
and output delays of 0–200 ps, 50 ps input slew, and 5 fF output load.

The comparison measures detailed placement with estimated parasitics.
Clock-tree synthesis, routing, extracted parasitics, and power estimation
are outside this flow. Area is placed standard-cell area. The reports show
setup and hold slack under the shared constraints. Meeting setup at 3 ns
corresponds to a nominal capacity of 333.3 million enabled vectors/s at that
clock, subject to implementation and timing sign-off.

`build/dot4-physical/` contains the verification evidence, mapped netlists,
placed DEF and ODB, timing reports, comparison TSV, tools, and input hashes.
This target evaluates the repository's reviewed source files directly.
