.PHONY: all check sim formal negative synth tools setup clean
all: check
check:
	bash scripts/check.sh all
sim formal negative synth tools:
	bash scripts/check.sh $@
clean:
	rm -rf build
setup:
	bash scripts/setup.sh
