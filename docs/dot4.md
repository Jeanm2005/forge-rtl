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

The baseline adds products left to right. The candidate uses two signed 17-bit
pair sums and a final signed 18-bit sum. Both use the same external contract.

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
