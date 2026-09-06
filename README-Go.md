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
