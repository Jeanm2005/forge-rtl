module reachability (
    input wire clk, rst, en,
    input wire signed [7:0] a, b,
    output wire ok,
    output wire signed [31:0] gold_acc, mutant_acc,
    output wire valid, effective_reset
);
    reg first = 1'b1;
    always @(posedge clk) first <= 1'b0;
    assign valid = !first;
    assign effective_reset = first | rst;
    mac_gold gold (.clk, .rst(effective_reset), .en, .a, .b, .acc(gold_acc));
    mac_mutant mutant (.clk, .rst(effective_reset), .en, .a, .b, .acc(mutant_acc));
    assign ok = first || (gold_acc == mutant_acc);
endmodule
