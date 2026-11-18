module dot4_gold (
    input  logic               clk,
    input  logic               rst,
    input  logic               en,
    input  logic        [31:0] a,
    input  logic        [31:0] b,
    output logic signed [17:0] result
);
    logic signed [15:0] product [0:3];
    logic signed [17:0] term [0:3];

    for (genvar lane = 0; lane < 4; lane++) begin : lanes
        assign product[lane] = $signed(a[8*lane +: 8]) * $signed(b[8*lane +: 8]);
        assign term[lane] = {{2{product[lane][15]}}, product[lane]};
    end

    always_ff @(posedge clk) begin
        if (rst)
            result <= 18'sd0;
        else if (en)
            result <= ((term[0] + term[1]) + term[2]) + term[3];
    end
endmodule
