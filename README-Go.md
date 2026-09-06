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

## Experiment 3: v1, and the shape that stuck

**1.742 s ± 0.019 s.** One `F_NOCACHE` `pread` reader per core over disjoint byte ranges, each worker folding into its own prefix-hashed linear-probe table, merged once at the end. 16.2× the naive skeleton at 100m rows.

The structure has not changed since. Everything after this is a change to how a worker walks its buffer, never to how the work is divided.

Splitting a file on byte offsets has four distinct off-by-one traps, and a single-configuration test finds none of them:

- a range starting exactly **on** a row boundary
- a range **shorter than one row**
- a **buffer no larger than its range**
- first-`;` versus first-`\n` when locating the boundary

One sentence fixes all four, and it belongs in a comment next to the code: **a worker owns every row that starts inside its range**, so it skips past the first newline unless its range starts at byte 0, and reads past its own end to finish the last row it owns. Finding them took a sweep of worker count × buffer size × split strategy × reader against the reference.

Two mechanisms were killed at this stage. A cursor-based work-stealing split measured **+21%** against the static split, because dynamic scheduling costs more than the imbalance it removes when worker wall spread is only 1.03. And a branchless temperature parse **lost 15.2% in its microbenchmark and won 11.4% at a billion rows**, which is the first sign in this study that microbenchmarks and end-to-end runs disagree.

## Experiment 4: calibrating the instrument

At this point two invocations returned contradictory verdicts for the same flag: **−8.64%** in one, **+12.01%** in another. Rather than argue, run the null.

Eight arms in one `hyperfine` invocation, all of them the same binary with the same flags:

```
1.660  1.690  1.745  1.772  1.796  1.811  1.841  2.010   (seconds)
```

Monotonically increasing, **+21.08% end to end**, with user CPU rising 16.65% for work that is provably identical. Every verdict pending at that moment was smaller than the spread of this null result.

The fix is a 20 s cooldown before every timed run, plus naming the incumbent **first and last**. Under it the two bracket slots agree to **2.54%** and user CPU to **0.69%**.

Two rules follow, and both are refusals:

**A wide bracket is a refusal, not a correction factor.** Subtracting the measured per-slot drift from a contaminated invocation "recovered" +19.18% for a flag that a clean re-measure scored at **+0.06%**; five of that invocation's six margins vanished. A model of a confound is a detector, not a licence to subtract it.

**A cheaper input does not rank arms.** Seven strategy arms compared at 1.4 GB and at 13.8 GB disagreed **seven times out of seven**: four inverted outright, three vanished into overlapping ranges. At 1.4 GB nothing is I/O bound; at 13.8 GB everything is. The harness now refuses any file but the 1b one unless `--mechanism-only` is passed, and stamps the output `NOT A VERDICT`.

Ask a harness to rank N copies of one thing before believing it about N different things. It costs 90 seconds.

## Experiment 5: oversubscribe the workers

**1.742 s → 1.613 s, −7.49%.** `runtime.NumCPU()` is the reflex worker count and it is wrong whenever a worker alternates blocking reads with compute: while it sits in `pread`, its core has nothing to run.

```go
// Workers block ~30% of their wall in pread, so extra runnable
// goroutines cover the stall. Measured -7.49%; a plateau, not a peak.
workers := runtime.NumCPU() * 4 / 3
```

20 and 30 workers did not separate, so the optimum is a plateau and the low end is the one to pin. Going the other way is expensive: dropping back to one worker per core later measured **+14.09%**, with parallel efficiency falling from 80.2% to 69.7%.

A related mechanism turned out to be the same lever spent twice. A per-worker prefetch goroutine, filling buffer B while the fold runs on buffer A, is worth **0%** on top of oversubscription. On its own with one worker per core it recovers 72.5% of what oversubscription recovers, so the two are substitutes: both exist to give a core something to run while a read is outstanding.

## Experiment 6: validate in the parsed domain

**1.613 s → 1.424 s.** The format check was four to six dependent byte compares per row, behind an unpredictable three-way branch. A profile priced it at **18.2% of all CPU**, against a prediction of 3-8% that had been carried as an unmeasured line item for most of the study.

The parse already produces the value. A legal temperature is one integer range test on a register that is already live.

```go
// Rejections fold into the same 8-byte word the parse reads, so the
// shape is established from bits already in a register.
v, next, ok := parseTempWordFrom(w)
if !ok || v < -999 || v > 999 { return errBadRow }
```

The generalisation: **any validation whose predicate is expressible over the parsed value is being paid twice when it runs over the bytes.** Price it before assuming it is free.

## Experiment 7: the batch tokenizer, and what a microbenchmark ranks

The largest microbenchmark win in the study, and it does not survive contact with the binary.

Four tokenizer kernels were built and measured on arm64. An 8-byte SWAR scan beat a per-row 16-byte NEON scan by 16.3-23.1%, with the cause measured directly: a **1.080 ns/row vector-to-general-register transfer**, which is most of a row's entire budget on this machine. A batch tokenizer in the shape used by high-throughput tokenizers, scanning a window and emitting a token stream, measured **−40.4%**, the biggest single win recorded anywhere in the study.

Integrated end to end it measured **+9.8%** (pure Go) and **+10.4%** (calling into the assembly). A **50-point swing**.

The mechanism is not mysterious once stated. The microbenchmark's baseline was a single-needle staged tokenizer writing a token stream. The binary's actual row loop is a dual-needle scan consuming the row in place. **The −40.4% was a true measurement against a program this study does not contain.**

The rule that comes out of it: before quoting a delta forward, name the baseline it was measured against, and check the system you are about to apply it to contains that baseline. This fails independently of the scale trap in Experiment 4. A smaller input does not rank arms; a differently-shaped baseline does not either.

Both arms remain shipped as `-kernel batch-swar` and `-kernel batch-neon`, because the transfer cost that killed them is an arm64 property.
