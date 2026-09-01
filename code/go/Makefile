.PHONY: all build test vet gate check bench clean

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

bench: gate
	ASSETS=$(ASSETS) bash ../../scripts/bench.sh

clean:
	rm -rf bin
