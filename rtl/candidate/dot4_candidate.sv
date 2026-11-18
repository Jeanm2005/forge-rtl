module dot4_candidate (
    input  logic               clk,
    input  logic               rst,
    input  logic               en,
    input  logic        [31:0] a,
    input  logic        [31:0] b,
    output logic signed [17:0] result
);
    logic signed [15:0] product [0:3];

    for (genvar lane = 0; lane < 4; lane++) begin : lanes
        assign product[lane] = $signed(a[8*lane +: 8]) * $signed(b[8*lane +: 8]);
    end

    logic signed [16:0] pair_lo, pair_hi;
    assign pair_lo = $signed({product[0][15], product[0]}) + $signed({product[1][15], product[1]});
    assign pair_hi = $signed({product[2][15], product[2]}) + $signed({product[3][15], product[3]});

    always_ff @(posedge clk) begin
        if (rst)
            result <= 18'sd0;
        else if (en)
            result <= $signed({pair_lo[16], pair_lo}) + $signed({pair_hi[16], pair_hi});
    end
endmodule
