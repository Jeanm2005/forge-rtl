.PHONY: all check sim formal negative synth tools clean
all: check
check:
	bash scripts/check.sh all
sim formal negative synth tools:
	bash scripts/check.sh $@
clean:
	rm -rf build
