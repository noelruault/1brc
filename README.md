# 1brc

The [One Billion Row Challenge](https://github.com/gunnarmorling/1brc) run as a measured study rather than a leaderboard entry: aggregate 1,000,000,000 weather rows (13,795,610,267 bytes) into per-station min/mean/max, and find out what actually governs the time.

One implementation per language, each with its own record. Same machine, same input, same method, so the numbers are comparable to each other even though none of them is comparable to a published leaderboard.

| language | unrestricted | idiomatic | portable idiomatic | record |
|---|---:|---:|---:|---|
| **Go** | **[1.233 s](1brc.go)** | **[1.388 s](1brc.go)** | **[1.904 s](1brc.go)** | [README-Go.md](README-Go.md) |
| Zig | not started | | | |
| JavaScript | not started | | | |

Three columns because the published rules for this challenge disagree about what an implementation may use, so each record reports against every bar from the same binary, the same correctness gate and one bracketed invocation. The Go row is **PROVISIONAL**, having been measured on battery. Each record states which rule set each of its numbers was measured against, and what the restriction cost.

## Machine of record

Apple M5 Pro, 15 logical cores, 24 GB RAM, APPLE SSD AP1024Z, macOS 26.5.2 (Darwin 25.5.0) arm64.

The input file is **53.5% of RAM**, so it cannot be held in page cache, and every run reads it from disk. `iostat` confirms 13.1 GB moving per run. Most published 1BRC numbers are page-cached or served from a RAM disk, which is a different measurement, so they are recorded here as facts about their own machines and never compared against these.

That storage state sets the floor every implementation here is priced against, and it is a property of the machine rather than of any language: **0.754 s**, 15 parallel uncached `pread`s over 13.8 GB. It is where an implementation would land if parsing were free.

## Method

Three rules, applied to every number in every language record.

1. **Correctness gates speed.** No timing is recorded for a binary that fails the byte-compare, and each arm is byte-compared with its own flags before it is timed. The gate runs upstream's twelve sample files first, ahead of anything self-generated.
2. **A delta exists only inside one bracketed invocation.** All arms in one `hyperfine` run, 20 s cooldown, incumbent named first and last. Incumbent slots more than 3% apart voids every arm in that invocation.
3. **Measured, derived or hypothesis.** Every claim carries its label. A comparative claim is a hypothesis until it is benchmarked on this machine.

Rule 2 is not decoration. Eight identical arms in one invocation, same binary and same flags, ranked **21.08% apart** monotonically. Ask a harness to rank N copies of one thing before believing it about N different things.

## What is here

- [`1brc.go`](1brc.go), the whole Go solution in one standard-library file, generated from `code/go` by [`scripts/amalgamate.py`](scripts/amalgamate.py)
- [`README-Go.md`](README-Go.md), the Go record, experiment by experiment
- [`00-overview.md`](00-overview.md), headline numbers and the study index
- [`01-definition.md`](01-definition.md) through [`09-result.md`](09-result.md), the numbered reports, each with a `*-data.txt` companion holding the raw output and the command that produced it
- [`07-experiment-ledger.md`](07-experiment-ledger.md), all 40 experiments with the prediction each one was registered against
- [`CORRECTIONS.md`](CORRECTIONS.md), every published figure a later measurement moved, corrected at every site that carried it
- [`PARKED.md`](PARKED.md), nine ideas with the number that parked them and a runnable trigger that would revive them
- [`code/`](code), the implementation `1brc.go` is generated from, plus the generator and the slow reference
- [`scripts/`](scripts), the measurement harness

Commit hashes cited inside the reports name the history those reports were written against and do not resolve in this checkout.

## Running it

```bash
go run 1brc.go -in measurements.txt # the solution, one file, no go.mod needed
bash scripts/check-correctness.sh   # upstream's 12 samples + a 10k-station stressor
make -C code/go bench               # the winners, bracketed, 3 runs each
make -C code/go bench RUNS=10       # verdict strength
bash scripts/lab-suite.sh           # all 12 groups, 32 arms
```

The measurement files are generated, not committed. `code/gen` reproduces every one byte for byte from its recorded seed and command.

**Every arm ever built is still reachable by flag, including the ones that lost.** A verdict is a fact about the machine that took it: mmap lost 5.6× here because Darwin's 16 KiB pages give 842,067 faults on a path that does not parallelise, the page cache lost because the file is half of RAM, and four row cursors lost to register pressure that a wider register file may not have. `lab-suite.sh` re-ranks all 32 arms on any other machine, and the interesting result is which rows flip.

## Licensing

Third-party material and its licensing is recorded in [`LICENSES.md`](LICENSES.md). The station table and the twelve sample files come from gunnarmorling/1brc under Apache-2.0.
