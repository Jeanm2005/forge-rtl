module dot4_candidate (
    input  logic               clk,
    input  logic               rst,
    input  logic               en,
    input  logic        [31:0] a,
    input  logic        [31:0] b,
    output logic signed [17:0] result
);
    logic signed [15:0] product [0:3];
    logic signed [17:0] term [0:3];
    logic signed [17:0] sum_lo, carry_lo, sum_hi, carry_hi;

    for (genvar lane = 0; lane < 4; lane++) begin : lanes
        assign product[lane] = $signed(a[8*lane +: 8]) * $signed(b[8*lane +: 8]);
        assign term[lane] = {{2{product[lane][15]}}, product[lane]};
    end

    assign sum_lo = term[0] ^ term[1] ^ term[2];
    assign carry_lo = ((term[0] & term[1]) | (term[0] & term[2]) | (term[1] & term[2])) << 1'b1;
    assign sum_hi = sum_lo ^ carry_lo ^ term[3];
    assign carry_hi = ((sum_lo & carry_lo) | (sum_lo & term[3]) | (carry_lo & term[3])) << 1'b1;

    always_ff @(posedge clk) begin
        if (rst)
            result <= 18'sd0;
        else if (en)
            result <= sum_hi + carry_hi;
    end
endmodule
