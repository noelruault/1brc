.PHONY: all build test vet gate check bench bench-file clean

ASSETS ?= /Users/noelruault/Downloads/1brc/1brc-assets

all: gate

build:
	go build -o bin/1brc .

vet:
	go vet ./...

test:
	go test ./...

# gate is exactly what spec.md requires of this module. Nothing else may be added here: the report gate and this target have to stay the same commands.
gate: vet build test

check: gate
	ASSETS=$(ASSETS) bash ../../scripts/check-correctness.sh

# The measured winners, re-timed against each other in ONE bracketed invocation.
# The incumbent is named FIRST and LAST on purpose: a bracket wider than 3% voids every arm in the run, and without it an arm's slot decides its rank (eight identical arms once ranked 21% apart).
# RUNS=3 is a re-check, not a verdict: a promotion still wants -r 10. Cost is arms x (RUNS+1) x (wall + 20 s cooldown), so about 5 minutes here.
RUNS ?= 3
bench: gate
	ASSETS=$(ASSETS) bash ../../scripts/experiment.sh \
	  -i 'make bench: the measured winners' \
	  -p 'sweep: re-times the shipped default against the arms that won their groups' \
	  -r $(RUNS) \
	  -a 'default.a=' \
	  -a 'lanes=-fold lanes' \
	  -a 'lanes-w24=-fold lanes -workers 24' \
	  -a 'default.b='

# The single-binary timing this target used to run, kept because spec.md's method rules still name it.
bench-file: gate
	ASSETS=$(ASSETS) bash ../../scripts/bench.sh

clean:
	rm -rf bin
