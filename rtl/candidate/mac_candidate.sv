module mac_candidate (
    input logic clk, rst, en,
    input logic signed [7:0] a, b,
    output logic signed [31:0] acc
);
    logic signed [15:0] product;
    logic signed [31:0] extended_product, next_acc;
    assign product = a * b;
    assign extended_product = {{16{product[15]}}, product};
    assign next_acc = acc + extended_product;
    always_ff @(posedge clk) begin
        if (rst) acc <= 32'sd0;
        else if (en) acc <= next_acc;
    end
endmodule
