# The One Billion Row Challenge in Go

Aggregate 1,000,000,000 weather measurements (13,795,610,267 bytes) into per-station min/mean/max, and find out how fast Go does it on one machine.

This document is the Go arm of the study. It states what was built, what each experiment measured, and what the numbers support. Sibling documents will cover the same problem in other languages, so the method here is written to be re-run rather than re-argued.

## Results, by what the implementation is allowed to use

"Fastest Go" has no single answer, because the two published rules for this challenge disagree about what counts. Both are stated by people who did the work, so this study reports against both rather than picking the flattering one. All three rows are the **same binary**, same gate, same bracketed invocation.

| tier | what it may use | how to run it | wall clock | user CPU |
|---|---|---|---:|---:|
| **[Unrestricted](go/unrestricted/1brc.go)** | `unsafe` pointer walks, `F_NOCACHE` | `go run go/unrestricted/1brc.go -in measurements.txt` | **1.233 s** | 14.88 s |
| **[Idiomatic](go/idiomatic/1brc.go)** (stdlib incl. `syscall`) | no `unsafe`, no asm, no cgo, no third-party | `go run go/idiomatic/1brc.go -in measurements.txt` | **1.388 s** | 17.10 s |
| **[Portable idiomatic](go/portable/1brc.go)** (no OS-specific calls) | also no `syscall`, no mmap | `go run go/portable/1brc.go -in measurements.txt` | **1.904 s** | 17.65 s |

Bracket on that invocation: 2.88% wall, 0.29% user CPU. Raw output in [`bench/2026-09-06T101734Z-idiomatic-vs-unrestricted.txt`](bench/2026-09-06T101734Z-idiomatic-vs-unrestricted.txt), which is stamped **PROVISIONAL**: it ran on battery, and this study reserves a headline for AC power.

**Each tier is one file**, around 500 lines, standard library only, no `go.mod` and no flag but `-in`. What a tier may use is what its file contains rather than what a flag selects, so there is no strategy switch to read past and no arm of another tier compiled into it. All three are byte-identical to the reference on upstream's twelve samples, on both 10m gate cases and on the full 1b file.

[`go/idiomatic/1brc.go`](go/idiomatic/1brc.go) and [`go/portable/1brc.go`](go/portable/1brc.go) are derived from [`go/unrestricted/1brc.go`](go/unrestricted/1brc.go) by [`scripts/derive-tiers.py`](scripts/derive-tiers.py), which swaps the pointer walk for the slice walk and then drops the `F_NOCACHE` call. The kernels, the table and the reader stay identical across the three that way, so a difference between two tiers is only ever the restriction that defines it, and `check-correctness.sh` fails if a derived file is stale.

The wall clocks above were measured on [`code/go`](code/go), and [`scripts/bench-tiers.sh`](scripts/bench-tiers.sh) re-takes them from the tier files themselves in one bracketed invocation, each arm byte-compared against the 1b reference before it is timed. The harness computes the bracket rather than leaving it to the reader, and stamps a run VOID when the two incumbent slots sit more than 3% apart, which is what the first attempt did at 7.78% on a machine carrying a dev server. `code/go` is the flag-driven board these three were specialized out of, where every arm and every test still lives: [`main.go`](code/go/main.go) the flags and the measured defaults behind them, [`reader.go`](code/go/reader.go) the worker split and the parallel `pread`, [`table.go`](code/go/table.go) the open-addressing table and the fold loops, [`kernel.go`](code/go/kernel.go) the SWAR delimiter scan and the branchless parse, [`batch.go`](code/go/batch.go) the batch tokenizers, one of which calls into a Plan 9 assembly module and so has no place in a single file.

**The two idiomatic bars, and why both exist.** [driquet](https://driquet.info/1brc-autoresearch/) sets it at *"idiomatic, stdlib-only Go. Goroutines and syscall are fair game; unsafe, assembly, cgo, and third-party dependencies are not."* [Ben Hoyt](https://benhoyt.com/writings/go-1brc/) sets it at *"portable Go using only the standard library: no assembly, no unsafe, and no memory-mapped files."* Our `F_NOCACHE` call is `syscall.Syscall(SYS_FCNTL, …)`, which is stdlib and needs no `unsafe`, so it passes the first bar and fails the second on **portability**: the constant is darwin-only. Turning it off skips the call entirely, and that arm uses no `syscall` package at all.

**What each restriction costs, which is the useful part:**

- **`unsafe` is worth 12.6% of wall and 14.9% of CPU.** Real, and smaller than its reputation. It buys the pointer walk; giving it up costs a seventh of the CPU.
- **Portability costs more than `unsafe` does: another 37 points of wall for only 3.7 points of CPU.** Dropping `F_NOCACHE` barely changes the compute and moves the time into system CPU and waiting, exactly as the read floor predicts.
- **Reaching for the stdlib map and a scalar parse costs +81.8% of CPU** (2.302 s), which is the rung both referenced write-ups climbed and which this study never shipped.

**Target 1.000 s, missed on every tier.** The compute floor is 0.939 s, below the target, and the gap between floor and clock is the read path. Nothing below is an assembly result: the assembly arms were built, measured, and lost.

## The finding that outlives the number

**Roughly half the results in this document reverse on different hardware, and the useful work was finding out which half.**

Every headline below is a verdict about one machine. The ones that flip are predictable from the hardware, which is what makes them worth writing down:

| result | verdict here | what it turns on | where it should invert |
|---|---|---|---|
| mmap vs parallel `read()` | mmap 5.6× slower | Darwin's 16 KiB pages, 842,067 serial faults | a kernel with huge pages |
| page cache vs uncached | page cache slower | the file is 53.5% of RAM | any machine with 64 GB |
| hand-written NEON vs SWAR | SWAR wins | arm64 has no `PMOVMSKB` | x86-64, one instruction for the mask |
| 4 row cursors | +2.93%, worse than 1 | register budget on this core | x86-64, where the same change is −8% |
| oversubscribing workers | −7.49% at 15 cores | core count and read-stall ratio | **measured inverted**: 2× is −3.21% here and +2.2% on a 10-core M5 |
| custom table vs stdlib map | custom wins 15.8% | 413 keys, 0.3% load factor | 10,000 keys, where the map wins by 12.81% |

The oversubscription row is the sharpest of these, because it is the only one where the inversion has been **measured rather than predicted**. Two studies ran the same experiment on the same generation of Apple silicon: 2× oversubscription measured **+2.2% on 10 cores** and **−3.21% on 15**. Opposite sign, and the only material difference is the core count. The mechanism is visible in the second channel: user CPU is flat to 0.86% across 15, 20, 24 and 30 workers, so the lever changes how much of the machine waits, never how much work it does.

And the converse carries the most weight. **A result that survives two different machines is about the mechanism rather than the laptop.** Dropping mmap for parallel `pread` was measured here at 5.6× and independently by [driquet](https://driquet.info/1brc-autoresearch/) at −52% on a different chip, different core count and a different file. That one generalises. The register-pressure results do not, and are not claimed to.

Two practices follow, and they are the reason this study is shaped the way it is.

**Nothing is ever deleted.** All 32 arms still ship behind their flags, including everything that lost, each carrying the number that killed it. `scripts/lab-suite.sh` re-ranks the whole set on any machine, and **the interesting output there is not the wall clock, it is which rows flip.**

**A killed idea records the baseline it was killed against.** `PARKED.md` entries carry a runnable revive trigger, because a mechanism rejected against one bottleneck is not rejected, it is waiting. driquet's run demonstrates it from the other side: a SWAR scan rejected at 1.9% was re-tried four rounds later, after fixing I/O made compute visible, and accepted at −7.3%. Same code, opposite verdict, and the only thing that changed was what was in the way.

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

## Experiment 8: the pointer walk

**1.424 s → 1.233 s ± 0.010 s**, user CPU down 13.49% for byte-identical output. The row loop walks the buffer with `unsafe.Add` instead of reslicing at every row.

The ceiling was measured before the work: `unsafe` pointer walks were worth **4-16%** on the scan in isolation, and **reslicing recovers about half of that in safe Go**, because re-anchoring the slice lets the compiler drop the bounds check. Verify with `-gcflags=-d=ssa/check_bce` rather than assuming.

One guard in that loop carries the entire over-read safety:

```go
// The ONLY thing bounding the load below. The incumbent's slice read
// would panic if it were loosened; this one reads past the buffer in silence.
if pos+sep+9 > n { break }
```

Related and measured: a Plan 9 assembly call costs **~1.93 ns** on arm64. That is most of a 14-byte row's budget, so assembly here is callable per chunk and never per row. It is also why the per-row NEON kernel could not win no matter how good the kernel was.

## Experiment 9: the hash table, and a regime that inverts

A prefix-hashed open-addressing table beats Go's built-in `map` by **15.8%** on the 413-station key set. On a 10,000-station stressor it **loses by 3.7%** in the same probe, and **12.81% of wall** end to end.

The end-to-end gap is larger than the probe gap, and the reason is not the probe at all: the custom tables allocate **120 MiB** that the map never does, and system CPU drops 5.48% on an identical I/O path. Retention, not lookup.

Two things follow. **The right structure is a property of the deployment, not of the code**, so the alternative stays behind `-table map` rather than being deleted. And a lookup microbenchmark measures the lookup while the deployment pays the retention.

The 10k file itself needed a decision. Holding *rows* constant would produce a 57.09 GB input at 2.22× RAM, whose read floor alone is 3.12 s, answering a different question. Holding *bytes* constant gives 241.6M rows at 13.80 GB, which reproduces the memory regime the 1b file establishes. When you change a key regime, hold the memory regime.

## Experiment 10: decomposing the remaining gap

At 1.233 s against a 1.000 s target, the question is where 233 ms lives. `wall × cores` is an identity, so the answer needs no model:

```
wall × cores  =  user CPU  +  system CPU  +  idle-core time
   18.03 s    =   14.09 s  +    1.84 s    +    2.10 s
    (100%)        (78.1%)      (10.2%)        (11.7%)
```

Reproduced to the digit across three unprofiled rounds. It closes a third of the search space immediately:

**The system share is the kernel copying 13.8 GB into the worker buffers.** It scales with bytes, not with the 13,160 calls, so no buffer size touches it. The only mechanism that removes a copy is mmap, already killed at 5.6×. Those points are **closed, not open**.

**The idle share is a supply problem.** 12.85 workers of 20 have work at any instant, coexisting with 0.63 workers' worth descheduled. Perfect packing computes to **1.0740 s**, still +7.40% over target. It is a candidate for the gap and never for the goal.

**The compute floor is 14.09 s over 15 cores = 0.939 s, already below the 1.000 s target.** So compute is not what makes the target unreachable.

Instrument costs, measured rather than assumed: `-cpuprofile` costs **+13.7% of wall clock** and **+0.015% of instructions retired** (279,161,109,302 against 279,120,176,115). A profiled run's *shares* rank functions; its *seconds* are not the binary's. The same counter gives the study its per-row figure: **279.1 instructions per row.**

The profile of the shipped default, for orientation rather than as a verdict:

```
      flat  flat%                        function
     5.94s 49.46%   syscall.rawsyscalln
     2.45s 20.40%   main.indexDelimAt
     1.34s 11.16%   main.parseTempWordFrom
     0.70s  5.83%   main.(*table).update      (cum 11.24%)
     0.59s  4.91%   runtime.memequal
```

## Experiment 11: two row cursors

**1.233 s → 1.202 s, and user CPU 14.85 s → 14.09 s.**

The row loop is latency-bound, not throughput-bound, and the profile does not show it. A row starts at `pos + sep + 1 + width`, and `width` is unknown until that row's own number is parsed and retired. One cursor gives the core a single serial dependency chain per row with no independent work to fill the stalls. Measured IPC is **≈4.5** (279.1 instructions/row over ~61.6 G cycles) on a roughly 8-wide core.

The fix is more chains, not fewer instructions. Split each buffer at a **row boundary** and advance two cursors in lockstep:

```go
// Both scans issue before either result is consumed. This is the whole
// kernel, and the reason the two are not folded into a helper called twice.
sepA, semiA, _ := indexDelimAt(rowA, endA-posA)
sepB, semiB, _ := indexDelimAt(rowB, endB-posB)
vA, nextA, okA := parseTempWordFrom(*(*uint64)(unsafe.Add(rowA, sepA+1)))
vB, nextB, okB := parseTempWordFrom(*(*uint64)(unsafe.Add(rowB, sepB+1)))
```

Correctness is free: each lane owns whole rows, and min/max/sum/count commute.

Measured across four independent invocations: **user CPU −5.15% to −5.28%**, against control brackets of 0.034% and 0.188%. Wall clock **−2.79%** against a **0.57%** bracket, and in the tightest invocation **−3.57%** against a **0.000%** bracket where both incumbent slots landed on 1.287 s to the millisecond.

Two things this measurement teaches beyond the win.

**A tight bracket does not imply tight arms.** On a busy machine, per-arm σ reached 4% while the incumbent slots agreed exactly, because the cooldown spreads every arm's runs across the same noise. So a delta can be quotable while its ranges still overlap. *Quotable* and *disjoint* are separate tests, and a default change wants the second one.

**The wall win is consistently smaller than the CPU win**, because removing compute makes whatever else gates the pipeline bind harder. Wall-above-floor rose from 29.8% to 32.0% in the same measurement that produced the win.

## Experiment 12: four cursors

**Killed. +1.74% wall, +2.93% user CPU** against a 0.57% bracket, which is worse than the *one*-cursor incumbent rather than merely worse than two.

The prediction registered before the run allowed for exactly this: either another 2-6% of CPU, or a plateau as register and cache pressure over four live rows takes back what the extra chains buy. A measured plateau was named in advance as the useful answer, and it is what arrived. The out-of-order window on this core is saturated at two.

The same change is worth **−8% in a Rust implementation on x86-64**. That is a register-budget difference, not a contradiction, and it is why `-fold lanes4` stays shipped rather than deleted.

The ILP direction is now **bounded, not open**: two cursors is the optimum here, and "more ILP" is no longer an available lever on this machine.

## Experiment 13: what `update` is made of

A perfect hash over 413 fixed keys is the obvious remaining idea, and published Rust work measures a 26% win from one. The profile attributes `(*table).update` 12.5-14% of CPU and `runtime.memequal` 8.8-9.9%, which looks like a 21-24% pot.

No profile can settle it, because the probe, the key compare and the update arithmetic inline into one symbol. Three benchmark variants fold the identical access sequence into the identical slots, pinned by a test asserting the slot a variant writes is the slot `update` would have chosen:

```
full update                        8.34 ns/op
probe + empty-slot check, no compare   1.78 ns/op
direct index (no hash, probe, compare) 1.65 ns/op
```

**In isolation the probe is 0.13 ns and the key compare with its pointer chase is 6.56 ns, 79% of `update`.** Which reads as a strong case for perfect hashing.

It is not, and that is the finding. The profile puts `update` at **1.76-1.97 ns/row in situ, 4.4× cheaper than the 8.34 ns measured alone.** The difference is memory-level parallelism: alone, `update` eats its cache miss with nothing to overlap; inside the fold loop that miss is covered by the scan and parse of neighbouring rows.

This predicts a result already measured. A quotiented 32-byte entry removes exactly that pointer chase and measured **+5.34% of wall**. A cost that is 79% of a function in isolation and already hidden in place cannot be bought back; you only pay the arithmetic you add trying.

**The general rule: a share measured in isolation is an upper bound on what removing it can buy, and the ratio between isolated and in-situ cost is the memory-level parallelism the surrounding loop provides for free.**

Perfect hashing stays parked, on a mechanism rather than an estimate. Both cheap variants above are a **ceiling and not a proposal**: they delete the hash and the fallback branch a real perfect hash must keep.

## Results

| | wall clock | user CPU | note |
|---|---:|---:|---|
| naive skeleton | 26.1 ns/row | | correct, single-threaded |
| v1, parallel uncached readers | 1.742 s | | the shape that stuck |
| oversubscribed workers | 1.613 s | | −7.49% |
| validation in the parsed domain | 1.424 s | | 18.2% of CPU removed |
| pointer walk | 1.233 s | 14.85 s | −13.49% user CPU |
| **two row cursors** | **1.202 s** | **14.09 s** | −5.15% user CPU, four reproductions |

Every row above is the unrestricted tier. The idiomatic tiers are measured separately at the top of this document: **1.388 s** stdlib-with-`syscall`, **1.904 s** fully portable.

**Target 1.000 s. Missed by 20.2%.**

The read floor is 0.754 s and the compute floor is 0.939 s, both below the target. The wall clock sits 32.0% above the compute floor, and that gap is 10.2% kernel byte-copy plus 11.7% idle cores.

**Reaching 1.000 s from here requires −17.58% of user CPU**, holding today's overhead structure. The only pot that size is the separator scan at 41.6-43.6% of compute, and both board mechanisms for it are spent: fusing the hash into the scan measured +0.81%, and the batch tokenizer measured +9.8%/+10.4%. No third mechanism is proposed here, because none has been measured.

### Comparison, and what it is worth

Published Go results sit at 3.44 s and 14 s; a Rust implementation reaches 0.90 s on six cores. **None of these is comparable to 1.202 s**, in either direction.

Those runs are page-cached or served from a RAM disk, so I/O is largely excluded, while every run here reads 13.8 GB off disk. This is 2026 silicon with 15 cores against 6- and 10-core machines from 2020 and 2021.

On the one metric that survives the hardware difference, this implementation loses clearly: **~15.9 core-seconds against the Rust implementation's ~5.4**, roughly three times the CPU for the same work. The wall clock here is competitive because the machine has more cores.

## Reproducing

```bash
bash 1brc/scripts/check-correctness.sh   # 12 upstream samples + 10k stations
make bench                               # the winners, bracketed, 3 runs each
make bench RUNS=10                       # verdict strength
bash 1brc/scripts/lab-suite.sh           # all 12 groups, 32 arms
```

**Every arm ever built is still reachable by flag, losers included**: `-fold slice|hash|ptr|both|lanes|lanes4`, `-kernel row|batch-swar|batch-neon`, `-parse branchless|scalar|word`, `-table combined|split|quot|map`, `-io pread|mmap`, `-split static|cursor`, `-fill off|sync|ahead`. A verdict is a fact about the machine that took it, and half the kill list above turns on 16 KiB pages, one register file, and a file that happens to be 53.5% of this machine's RAM. `lab-suite.sh` re-ranks all 32 on any other box.

A registry guard keeps that honest: adding a kernel without registering it fails the arm count, and deleting one the flag still accepts fails the parse. Every arm meets the same differential corpus, including inputs no generated data can produce, such as a separator inside a station name.

The harness refuses rather than warns. It takes an exclusive lock, waits out a busy machine, stamps any run it could not verify as quiet, and voids every arm in an invocation whose incumbent slots disagree by more than 3%.

## Full record

- [`01-definition.md`](01-definition.md), the rules, read from upstream's source rather than its prose
- [`02-baseline.md`](02-baseline.md), the physical floor
- [`03-technique-recon.md`](03-technique-recon.md), technique inventory from the top entries
- [`04-asm-kernels.md`](04-asm-kernels.md), four arm64 tokenizer kernels measured
- [`05-go-techniques.md`](05-go-techniques.md), unsafe, BCE, hashing, sharding
- [`06-cross-disciplinary-transfer.md`](06-cross-disciplinary-transfer.md), mechanisms borrowed from other fields
- [`07-experiment-ledger.md`](07-experiment-ledger.md), all 40 experiments, each with its prediction
- [`08-method-what-worked.md`](08-method-what-worked.md), the method retrospective
- [`09-result.md`](09-result.md), the closing statement
- [`CORRECTIONS.md`](CORRECTIONS.md), every published figure that a later measurement moved
- [`PARKED.md`](PARKED.md), nine ideas with the number that parked them and a runnable revive trigger
