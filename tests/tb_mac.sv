module tb_mac;
    timeunit 1ns;
    timeprecision 1ps;
    logic clk = 0;
    logic rst, en;
    logic signed [7:0] a, b;
    wire signed [31:0] gold, candidate;
    logic [31:0] expected = 0;
    logic [31:0] rng = 32'h1badf00d;
    int unsigned cycle = 0;
    int unsigned positive_wraps = 0, negative_wraps = 0;
    mac_gold g (.clk, .rst, .en, .a, .b, .acc(gold));
    mac_candidate c (.clk, .rst, .en, .a, .b, .acc(candidate));

    task automatic tick(input bit reset_value, enable_value,
                        input logic signed [7:0] av, bv);
        longint signed wide, left_value, right_value;
        clk = 0;
        rst = reset_value;
        en = enable_value;
        a = av;
        b = bv;
        left_value = longint'($signed(av));
        right_value = longint'($signed(bv));
        wide = longint'($signed(expected));
        if (rst) wide = 0;
        else if (en) begin
            wide = wide + left_value * right_value;
            if (wide > 64'sd2147483647) positive_wraps++;
            if (wide < -64'sd2147483648) negative_wraps++;
        end
        expected = wide[31:0];
        #5;
        clk = 1;
        #1;
        cycle++;
        if (gold !== expected || candidate !== expected)
            $fatal(1, "cycle=%0d rst=%b en=%b a=%0d b=%0d expected=%h gold=%h candidate=%h",
                   cycle, rst, en, a, b, expected, gold, candidate);
        #4;
    endtask

    function automatic logic [31:0] advance(input logic [31:0] state);
        logic [31:0] x;
        x = state ^ (state << 13);
        x = x ^ (x >> 17);
        return x ^ (x << 5);
    endfunction

    initial begin
        tick(1, 1, -8'sd128, -8'sd128);
        tick(0, 1, 8'sd127, 8'sd127);
        tick(0, 0, -8'sd128, 8'sd127);
        tick(0, 1, -8'sd128, 8'sd127);
        tick(0, 1, 8'sd127, -8'sd128);
        tick(0, 1, 8'sd0, -8'sd128);
        tick(1, 0, 8'sd1, 8'sd1);
        // Legal repeated products cross each signed boundary without state injection.
        repeat (131073) tick(0, 1, -8'sd128, -8'sd128);
        tick(1, 1, -8'sd1, 8'sd1);
        repeat (132105) tick(0, 1, -8'sd128, 8'sd127);
        tick(1, 1, 8'sd127, 8'sd127);
        for (int i = 0; i < 10000; i++) begin
            rng = advance(rng);
            tick(rng[31:24] == 8'h00, rng[16], $signed(rng[7:0]), $signed(rng[15:8]));
        end
        if (positive_wraps == 0 || negative_wraps == 0)
            $fatal(1, "missing wrap coverage positive=%0d negative=%0d", positive_wraps, negative_wraps);
        $display("PASS cycles=%0d seed=1badf00d positive_wraps=%0d negative_wraps=%0d", cycle, positive_wraps, negative_wraps);
        $finish;
    end
endmodule
