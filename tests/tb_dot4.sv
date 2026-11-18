module tb_dot4;
    logic clk = 1'b0;
    logic rst, en;
    logic [31:0] a, b;
    logic signed [17:0] gold_result, candidate_result;
    int signed expected = 0;
    logic [31:0] rng = 32'h7d82_a134;

    dot4_gold gold (.result(gold_result), .*);
    dot4_candidate candidate (.result(candidate_result), .*);

    function automatic logic [31:0] next_word();
        rng ^= rng << 13;
        rng ^= rng >> 17;
        rng ^= rng << 5;
        return rng;
    endfunction

    task automatic drive(input logic reset, enable, input logic [31:0] x, y);
        rst = reset;
        en = enable;
        a = x;
        b = y;
        if (reset || enable)
            expected = 0;
        if (!reset && enable)
            for (int lane = 0; lane < 4; lane++)
                expected += int'($signed(x[8*lane +: 8])) * int'($signed(y[8*lane +: 8]));
        #5 clk = 1'b1;
        #5;
        if (int'(gold_result) !== expected || int'(candidate_result) !== expected)
            $fatal(1, "a=%h b=%h rst=%b en=%b expected=%0d gold=%0d candidate=%0d",
                   a, b, rst, en, expected, gold_result, candidate_result);
        clk = 1'b0;
    endtask

    initial begin
        drive(1, 0, '0, '0);
        drive(0, 1, 32'h80808080, 32'h80808080);
        drive(0, 0, '0, '0);
        drive(0, 1, 32'h80808080, 32'h7f7f7f7f);
        drive(1, 1, '1, '1);
        drive(0, 0, '1, '1);
        drive(0, 1, 32'h817f8001, 32'hfefdfcfb);
        for (int cycle = 0; cycle < 4096; cycle++)
            drive(cycle % 97 == 0, cycle % 7 != 0, next_word(), next_word());
        $display("PASS dot4 scoreboard (seed 7d82a134, 4103 cycles)");
        $finish;
    end
endmodule
