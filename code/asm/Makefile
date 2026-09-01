.PHONY: all fmt vet test bench gate

# `make test` is the gate spec.md names for this module; it must stay these commands.
# The `test -z` matters: `gofmt -l` exits 0 even when it lists unformatted files.
fmt:
	test -z "$$(gofmt -l .)"

vet:
	go vet ./...

test: fmt vet
	go test ./...

all: test

gate: test

# bench feeds 04-asm-kernels-data.txt. -count 5 so the report can quote a stddev.
bench:
	go test -run '^$$' -bench . -benchmem -count 5
