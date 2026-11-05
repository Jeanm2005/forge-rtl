module mac_formal (
    input wire clk, rst, en,
    input wire signed [7:0] a, b,
    output wire signed [31:0] acc
);
    reg first = 1'b1;
    wire signed [31:0] raw_acc;
    always @(posedge clk) first <= 1'b0;
    // Trusted reset prefix; only the unspecified pre-reset observation is masked.
    assign acc = first ? 32'sd0 : raw_acc;
    `DUT dut (.clk(clk), .rst(first | rst), .en(en), .a(a), .b(b), .acc(raw_acc));
endmodule
