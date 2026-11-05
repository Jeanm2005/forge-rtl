module mac_gold (
    input logic clk, rst, en,
    input logic signed [7:0] a, b,
    output logic signed [31:0] acc
);
    always_ff @(posedge clk) begin
        if (rst) acc <= 32'sd0;
        else if (en) acc <= acc + (32'($signed(a)) * 32'($signed(b)));
    end
endmodule
