module mac_mutant (
    input logic clk, rst, en,
    input logic signed [7:0] a, b,
    output logic signed [31:0] acc
);
    logic signed [15:0] product;
    assign product = a * b;
    always_ff @(posedge clk) begin
        if (rst) acc <= 32'sd0;
        else if (en) acc <= acc + $signed({16'b0, product});
    end
endmodule
