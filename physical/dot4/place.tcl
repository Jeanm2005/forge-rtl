set_thread_count 1
read_lef $::env(DOT4_TECH_LEF)
read_lef $::env(DOT4_CELL_LEF)
read_liberty $::env(DOT4_LIBERTY)
read_verilog mapped.v
link_design dot4
read_sdc $::env(DOT4_SDC)

initialize_floorplan -die_area {0 0 80 80} -core_area {3.04 4.2 77 77} -site FreePDK45_38x28_10R_NP_162NW_34O
make_tracks
place_pins -hor_layers metal3 -ver_layers metal2
set_wire_rc -signal -layer metal3
set_wire_rc -clock -layer metal3
global_placement -density 0.55
detailed_placement
check_placement -verbose
estimate_parasitics -placement
check_setup -verbose
report_checks -path_delay max -format full_clock_expanded -digits 4 > setup.rpt
report_checks -path_delay min -format full_clock_expanded -digits 4 > hold.rpt
report_worst_slack -max -digits 4
report_worst_slack -min -digits 4

set block [ord::get_db_block]
set dbu [$block getDbUnitsPerMicron]
set area 0.0
foreach inst [$block getInsts] {
    set master [$inst getMaster]
    set area [expr {$area + double([$master getWidth]) * [$master getHeight] / ($dbu * $dbu)}]
}
set metrics [open metrics.tsv w]
puts $metrics "stage\tpost-detailed-placement"
puts $metrics "cell_area_um2\t[format %.4f $area]"
puts $metrics "setup_wns_ns\t[format %.4f [sta::worst_slack -max]]"
puts $metrics "hold_wns_ns\t[format %.4f [sta::worst_slack -min]]"
close $metrics
write_def placed.def
write_db placed.odb
