# The One Billion Row Challenge in Go

Aggregate 1,000,000,000 weather measurements (13,795,610,267 bytes) into per-station min/mean/max, and find out how fast Go does it on one machine.

This document is the Go arm of the study. It states what was built, what each experiment measured, and what the numbers support. Sibling documents will cover the same problem in other languages, so the method here is written to be re-run rather than re-argued.

**Result: 1.202 s ± 0.032 s**, against a self-imposed 1.000 s target. The target is missed by 20.2%. The compute floor is 0.939 s, below the target, and the gap between the two is the read path.

## The machine of record

Every number in this document was measured on:

| | |
|---|---|
| CPU | Apple M5 Pro, 15 logical cores (5 "Super", 10 "Performance") |
| Memory | 24 GB |
| Storage | APPLE SSD AP1024Z, ~10.6 GB/s sustained measured under load |
| OS | macOS 26.5.2, Darwin 25.5.0, arm64 |
| Toolchain | go1.27.0 darwin/arm64 |
| Input | 413 stations, uniformly sampled, Gaussian temperatures, 13.80 bytes/row |
| Storage state | uncached (`F_NOCACHE`), read from disk on every run |

The storage state matters more than it looks. The file is 53.5% of RAM, so it cannot be held in page cache, and `iostat` confirms 13.1 GB moving off disk during a 1.2 s run. Published 1BRC numbers are usually page-cached or served from a RAM disk. These are not.

## What the challenge requires

Input rows are `Station;-12.3`: a UTF-8 name, a semicolon, a temperature with exactly one decimal in `-99.9..99.9`. Output is `{Abha=-23.0/18.0/59.2, ...}`, stations sorted, each value rounded to one decimal.

The rounding is the part that bites. Upstream rounds toward positive infinity via Java's `Math.round`, which is `floor(x + 0.5)`, so `-2.45` becomes `-2.4`. Go's `math.Round` is round-half-away-from-zero and disagrees on every negative tie. The mean is rounded **twice**: `round(round(sum) / count)`. And `-0.0` can never appear, because `Math.round` returns a `long`. This implementation aggregates in integer tenths, where none of those three traps can arise.

Correctness is checked against upstream's own twelve sample files (`measurements-rounding`, `measurements-boundaries`, `measurements-complex-utf8`, and nine others), plus a 10,000-station stressor. They gate every build, ahead of any self-generated case, and pass 12/12.

## Method

Three rules govern every number here.

**Correctness gates speed.** No timing is recorded for a binary that fails the byte-compare. Each experiment arm is byte-compared *with its own flags* before it is timed.

**A delta only exists inside one bracketed invocation.** All arms run in a single `hyperfine` invocation with a 20 s cooldown, and the incumbent is named first *and* last. If the two incumbent slots disagree by more than 3%, no arm in that invocation may be quoted.

**Measured, derived, hypothesis.** Every claim carries its label. A comparative claim is a hypothesis until benchmarked on this machine, and leaderboard timings from other hardware are never compared against these as though the hardware were the same.

## Experiment 1: the physical floor

Before writing a fast implementation, measure how long it takes to merely touch 13.8 GB.

| how the bytes are read | 1b wall clock | GB/s |
|---|---|---|
| `read()`, 1 MiB × 15 parallel readers, uncached | **754.4 ms ± 8.8 ms** | 18.29 |
| `read()`, 1 MiB × 8 parallel readers, page-cached | 1.126 s ± 0.007 s | 12.25 |
| `read()`, 1 MiB, single reader, page-cached | 1.221 s | 11.30 |
| `dd bs=1m`, page-cached | 1.343 s | 10.28 |

Two results, both counter to the usual advice.

**The page cache loses.** The file is 12.85 GiB against 24.00 GiB of RAM. After two sequential passes, free memory is 57.8 MiB and only 9.47 GiB sits on the active+inactive lists, so macOS evicts the head of the file while the tail is being read. Requesting *uncached* reads with `fcntl(fd, F_NOCACHE, 1)` is 1.49× faster than letting the cache try.

**Parallel readers scale where a single one does not.** 15 readers reach 18.29 GB/s where one reaches 11.30.

The floor sets the budget: **1.000 s over 1e9 rows is 1.00 ns/row, or 13.80 GB/s.** The read alone consumes 0.754 s of that. Whatever the parser does, it does it in the remaining 246 ms, or it overlaps with the read.

A note on what this number is not. `F_NOCACHE` prevents *new* caching but does not purge pages already resident, so it is labelled **uncached**, never *cold*. A true cold measurement needs `purge`, which needs sudo, and no such number exists in this study.

## Experiment 2: mmap, and why the usual answer is wrong here

Nearly every top 1BRC entry memory-maps the file. All of them run on Linux, most with huge pages available. Measured here, mmap is **5.6× slower end to end** than parallel `read()`, and the mechanism is not memory pressure.

The tempting explanation is that the file exceeds what RAM can hold. That explanation is wrong, and a smaller file disproves it: on a 137 MB file, entirely resident, mmap still reaches only **17.25 GB/s against 59.20 GB/s** for `read()` plus a count. mmap scales **1.15×** from 1 to 8 workers where `read()` scales **3.6×**.

The cause is the fault path. Darwin uses 16 KiB pages, so the 1b file takes **842,067 faults**, and that path does not parallelise. `MADV_WILLNEED` makes it worse rather than better.

**This is a property of this operating system, not of mmap.** On a kernel with transparent huge pages the fault count drops by three orders of magnitude and the ranking may invert. The arm stays shipped behind `-io mmap` for exactly that reason.
