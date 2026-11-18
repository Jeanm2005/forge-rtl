create_clock -name core -period 3.000 [get_ports clk]
set_clock_uncertainty 0.050 [get_clocks core]
set inputs [get_ports {a* b* en rst}]
set_input_delay -clock core -max 0.200 $inputs
set_input_delay -clock core -min 0.000 $inputs
set_input_transition 0.050 $inputs
set outputs [get_ports {result*}]
set_output_delay -clock core -max 0.200 $outputs
set_output_delay -clock core -min 0.000 $outputs
set_load 5.000 $outputs
