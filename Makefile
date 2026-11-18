.PHONY: all check sim formal negative synth tools setup clean worker-check control-check proposal-check
all: check
check:
	bash scripts/check.sh all
sim formal negative synth tools:
	bash scripts/check.sh $@
clean:
	rm -rf build
setup:
	bash scripts/setup.sh
worker-check:
	bash scripts/worker-check.sh
control-check:
	bash scripts/control-check.sh
proposal-check:
	bash scripts/proposal-check.sh

.PHONY: dot4-check
dot4-check:
	bash scripts/dot4-check.sh

.PHONY: dot4-physical
dot4-physical:
	bash scripts/dot4-physical.sh
