# 02 — Data and baseline: the physical floor on this machine

Raw output and the command behind every number: [`02-baseline-data.txt`](02-baseline-data.txt). Nothing below is quoted from anywhere; every figure was measured in one session on an idle machine, and every claim is labelled **measured**, **derived** or **hypothesis** per the method rules.

## The question this report answers

`01-definition.md` fixed the target: aggregate 1,000,000,000 rows of `station;temperature` into min/mean/max per station, and do it in under 1.0 s wall clock on this machine. Before writing a single line of a fast implementation, we need the floor: how long does it take to merely *touch* 13,795,610,267 bytes here? Everything a solution does beyond touching the bytes is spent out of whatever is left.

## The machine

Apple M5 Pro, 15 physical cores, 24.00 GiB RAM, macOS 26.5.2, arm64, go1.27.0. Two details turn out to matter more than the core count.

`hw.pagesize` is **16384**, not 4096. A 12.85 GiB mapping is 842,067 pages, and every one of them is a fault the first time it is touched. This is the reason mmap loses here (below).

The two performance levels are named **Super** (5 cores) and **Performance** (10 cores) — this chip reports no Efficiency level, so all 15 cores are worth scheduling parse work on. That is unusual enough to state explicitly: on an M1/M2/M3/M4 the E-cores are markedly slower and a naive 1:1 shard-per-core split underperforms; here the asymmetry is between two fast tiers, not fast and slow. **Hypothesis** (untested): a uniform 15-way split is close to optimal on this chip, where on earlier Apple silicon it would not be.

## The premise the official eval assumes does not hold here

The official 1BRC eval ran with the measurements file resident in the page cache (their box: 32-core EPYC, 128 GB RAM). Our file is **53.5% of physical RAM** (12.85 GiB of 24.00 GiB), and macOS will not keep it: after two full sequential passes, free memory is down to 57.8 MiB and only 9.47 GiB is on the active+inactive lists. The head of the file is evicted while the tail is being read.

So every 1b timing on this machine pays real storage bandwidth. This directly conflicts with `spec.md:35` ("Headline number = warm page cache, matching the official eval method"): the state that rule names is not reachable here for the 1b file. The report gives both numbers, keeps the page-cached one as the spec-designated headline, and flags the conflict rather than quietly redefining the rule. **Measured.**

A purged-cache ("cold") number was not taken at all: `purge` needs sudo and this loop runs unattended. In its place the uncached path is measured directly with darwin's `F_NOCACHE` (`fcntl(fd, 48, 1)`), which tells the kernel not to retain this descriptor's pages. That is not the same as a purged cache, so it is reported as **uncached**, never as cold. Evidence it took effect: three back-to-back 13.8 GB uncached passes left 3.53 GiB free, where the same three passes without it drove free memory to 57.8 MiB.

## The floor

**Every number in this report is PROVISIONAL.** `spec.md:42` requires the power source and load to be recorded with each measurement and labels battery numbers provisional; this session ran on battery (88%, discharging) at load average 4.29. macOS caps sustained CPU and SSD power on battery, so the true floor on AC is expected to be at or below these figures, and the *ordering* of the strategies is the load-bearing result rather than any single figure. The headline must be re-measured on AC before `final-dod` publishes it. Power and load are recorded in the data companion.

| what | 1b wall clock | GB/s | ns/row |
|---|---|---|---|
| `read()` 1 MiB × 15 parallel readers, **uncached** | **754.4 ms ± 8.8 ms** | 18.29 | 0.754 |
| same, plus `bytes.Count('\n')` over every byte | 768.8 ms ± 7.9 ms | 17.94 | 0.769 |
| `read()` 1 MiB × 8 parallel readers, page-cached (spec headline) | 1.126 s ± 0.007 s | 12.25 | 1.126 |
| `read()` 1 MiB, single reader, page-cached | 1.221 s | 11.30 | 1.221 |
| `dd bs=1m`, page-cached | 1.343 s | 10.28 | 1.343 |
| `mmap` + parallel newline count, best of 1/4/8/15/30 workers | 6.75 s | 2.04 | 6.75 |
| `wc -l` (BSD) | 7.41 s | 1.86 | 7.41 |
| `bufio.Scanner` line loop, single-threaded | 11.30 s | 1.22 | 11.30 |
| naive Go aggregator (`cmd/reference`), single-threaded | ~40 s *(derived from 3.98 s at 100m)* | 0.35 | 39.8 |

All hyperfine rows are 5 runs after 1 warmup; the raw output is in the data companion. Every `count` run independently reported `lines=1000000000`, which agrees with `wc -l` and with the generator's own row count.

## Three results that change what we build

**1. Uncached beats the page cache by 1.49x, and that inverts the usual advice.** 754 ms with `F_NOCACHE` against 1.126 s through the page cache, both at 1 MiB × parallel readers, both steady-state under hyperfine with σ under 10 ms. For a file larger than RAM the page-cache path pays to install and then evict 12.85 GiB of pages, and that bookkeeping costs more than it ever returns, because nothing is read twice. **Measured.** The actionable form: the fast path here is parallel `pread` with `F_NOCACHE`, not a warmed cache.

**2. mmap is 5-9x slower than `read()` on a scan, and it is not simply a memory-pressure effect.** (A scan result, so a prior and not a verdict — see the feasibility section.) Best mmap number on 1b is 6.75 s against 853 ms for a page-cached read. The tempting explanation is that the file exceeds RAM — but the 137 MB file, which is entirely resident, still only reaches 17.25 GB/s under mmap against 59.20 GB/s for `read()`+count, and mmap scales just 1.15x from 1 to 8 workers where `read()` scales 3.6x. So the fault path itself is the bottleneck and it does not parallelise. With 16 KiB pages and 842,067 faults for the 1b file, that is where the time goes. **Measured**, on a scan. This matters because mmap is the near-universal choice in the top 1BRC entries — all of them on Linux, most with huge pages available. Whatever we port from them, the I/O strategy does not port unexamined.

There is a repeatable oddity inside the mmap numbers: **workers=4 is a pessimum**, 18.46 s and 18.00 s on two independent runs, worse than workers=1 (9.85 s) and 2.6x worse than workers=15 (6.90 s). It reproduces, so it is a property of the path, not noise. Its cause is not established — 4 concurrent fault streams defeating fault-ahead is a **hypothesis**, and one worth killing or confirming before anyone reaches for mmap again.

**3. Finding the row boundaries is nearly free; everything else is not.** Newline detection over all 13.8 GB costs **+14.4 ms** on top of the read (768.8 vs 754.4 ms), which is 1.9%. `bytes.Count` compiles to the arm64 SIMD `memchr`-alike, so the naive scalar/SWAR/NEON question for *newline finding alone* is already answered: the stdlib is at memory bandwidth. The tokenizer work in `asm-recon`/`asm-kernels` therefore has to be aimed at the composite operation — find `;` and `\n`, hash the name and parse the temperature in one pass over the bytes — not at newline finding in isolation, which has no headroom left in it.

## What <1.0 s implies, arithmetically

```
target                 1.000 s / 1e9 rows   =  1.00 ns/row   =  13.80 GB/s
measured read floor    0.754 s              =  0.754 ns/row  =  18.29 GB/s
headroom               0.246 s              =  24.6% of the budget
aggregate CPU budget   15 cores x 1.0 s     =  15.0 ns/row of core time
read path's own CPU    30 ms user + 1075 ms sys  =  1.10 ns/row of that 15.0
left for the work      13.9 ns/row of core time, if parse fully overlaps I/O
```

At ~3.7 GHz that is roughly **51 cycles of core time per row**, for ~13.8 bytes of input: find two delimiters, hash a 1-100 byte name, parse a signed fixed-point temperature, and fold it into a per-shard accumulator. **Derived**, from the measured floor and the core count.

Two reference points for how far that is from where we stand. `bufio.Scanner` costs 11.30 ns/row single-threaded, so *line iteration alone*, spread perfectly over 15 cores, would consume 0.75 ns/row — **75% of the entire wall-clock budget** to do nothing but hand out lines. And the naive aggregator costs 39.8 ns/row, which even with perfect 15-way scaling and free I/O lands at 2.65 s, **2.65x over budget**. Both **derived** from measured single-threaded rates; neither has been run in parallel yet.

## Verdict on feasibility

Under 1.0 s is **not excluded** by this machine's physics: the bytes can be delivered in 754 ms, leaving 246 ms and ~13.9 ns/row of core time. It is also not comfortable — the I/O floor alone is 75% of the budget, so the parse must overlap the read almost perfectly, and any strategy that reads the file twice, or through the page cache, or through mmap, is over budget before it parses anything. **Derived.**

What this hands to `go-v1-parallel` is a **starting point and three hypotheses, not a decision**. `spec.md:35` and `spec.md:37` are explicit that I/O strategy is a ledger question and that nothing is discarded on anything short of an end-to-end measurement, and every number on this page is a *scan* — read the bytes, maybe count newlines, do no aggregation. A scan is not a solution: once 15 shards are also writing into hash tables, they compete with the readers for the same memory bandwidth, and the ranking above can change. So:

- **Start from** parallel `pread`, `F_NOCACHE`, 1 MiB buffers, 15 readers: the fastest scan measured here, 754.4 ms.
- **H-io-1:** F_NOCACHE still beats the page cache end-to-end, with aggregation running. *Not yet measured.* The scan margin is 1.49x, which is large enough to expect it survives, and small enough that it might not.
- **H-io-2:** mmap stays slower end-to-end. **mmap is NOT killed** — it lost a scan benchmark by 5-9x, which is a strong prior and not a discard. Its one plausible rescue is that a fault-driven reader may overlap aggregation differently than a `read()`-driven one, and `madvise(MADV_WILLNEED)` was never tried. It goes in the ledger as an alternative to measure, per `spec.md:50` item 6.
- **H-io-3:** 1 MiB is at or near the optimum. Only 1 MiB and 8 MiB were measured (8 MiB is 1.39x worse); 64 KiB, 256 KiB and 4 MiB are unswept.

## Threads left open

- The mmap `workers=4` pessimum reproduces but is unexplained. Worth one experiment (`madvise(MADV_WILLNEED)`, or per-worker `mmap` of its own range instead of one shared mapping) before mmap is ruled out permanently rather than provisionally.
- `F_NOCACHE` was measured on `read()`/`pread` only. Whether it helps or hurts an mmap-based reader is untested.
- 1 MiB buffers beat 8 MiB by 1.39x; 64 KiB / 256 KiB / 4 MiB were not swept. The optimum inside that range is unknown, and it is cheap to find.
- Read bandwidth was measured with `read()` into a Go slice, which copies. Whether a `pread` into a hugepage-backed or pre-faulted buffer avoids any of the 1.075 s of system time is untested.
- The whole-file scan rates assume the solution reads the file exactly once. Nothing here measures what happens when 15 shards each also write into a per-shard hash map, i.e. when the read competes with the aggregation for memory bandwidth. That is `go-v1-parallel`'s first measurement.
