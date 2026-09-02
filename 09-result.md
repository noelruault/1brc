# 09 — Result: 1.233 s, and why the last 233 ms did not come off

The study's closing report. It states the measured best, the gap, the bottleneck with a profile behind it, and what is left parked; `spec.md:61` makes an honest miss with the ledger intact a valid research outcome and an unmeasured claim not one, so this report is written in those terms. Nothing here is new work: every figure points at the ledger row that measured it, and the commands are in [`09-result-data.txt`](09-result-data.txt).

## The answer

**A Go program that reads 13,795,610,267 bytes, parses 1,000,000,000 rows and aggregates 413 stations in 1.233 s ± 0.010 s on an Apple M5 Pro (15 cores, 24 GB).** The target was 1.000 s. **NOT REACHED, by +0.233 s, +23.3%.**

| | measured | source |
|---|---|---|
| **headline, 1b rows** | **1.233 s ± 0.010 s**, range 1.220-1.248, 5 runs, 20 s cooldown, bracket 0.072% | E-27 |
| the same binary re-measured a day later, control bracket, spread **0.000%** | **1.257 s** (arms 1.257 / 1.257, ranges 1.247-1.266 and 1.244-1.270) | E-36 |
| storage state | **uncached**: `F_NOCACHE` via darwin `fcntl(fd, 48, 1)`, 20 parallel 1 MiB `pread`s. Never called "cold": `purge` needs sudo and this loop is unattended, so no cold number exists in this study | `02-baseline.md`, E-27 |
| reader configuration | `-workers 20 -buf 1024 -split static -io pread -nocache=true`, and for the rest of the shipped default `-parse word -fold ptr -table combined -bits 17 -kernel row -fill off` | E-27 |
| machine state | AC power, `require_quiet` satisfied, measurement lock held, orphan check clear. **Not provisional** | E-27, E-36 |
| compute floor at perfect parallelism | **0.9812 s** (E-27) and **0.9556 s** (E-36), both BELOW the 1.000 s target | E-27, E-36 |
| the read floor it hides behind | 0.754 s, 15 parallel `F_NOCACHE` `pread`s | `02-baseline.md` |
| what v1 started at | 1.742 s ± 0.019 s | `go-v1-parallel` |
| the skeleton it replaces | 2.607 s at 100m, 26.1 ns/row | `bench/2026-09-01T125649Z-skeleton.txt` |

**Two figures, and the study publishes both.** E-27's 1.233 s reproduced nine times across five invocations inside a 2.31% spread, all on one night; three invocations at `final-dod` time, on the byte-identical binary (`git log <E-34 commit>..HEAD -- 1brc/code/go` returns zero commits), measured 1.345 s unbracketed, 1.2865 s bracketed, and 1.257 s in a control bracket with a 0.000% spread, every one of them disjoint from the published band. `CORRECTIONS.md` C13 corrects the "no drift" claim at both sites that made it. **The conclusion does not turn on which figure is taken**: the target is missed by 23.3% at 1.233 s and by 25.7% at 1.257 s, and the reason is the same in both.

The direction of the two CPU channels is what makes this a measurement about the machine and not about the program: today's four bracketed arms spend **2.26% less user CPU and 16.21% less system CPU** than E-34's two, on identical code, while taking more wall. A slower program spends more CPU, not less. No mechanism is named for the residual, which is the same refusal round 3 made about the separator scan.

## Correctness, which comes before any of it

`spec.md:37`: a variant that fails byte-compare is not a result, it is a bug, and the correctness gate runs before any benchmark is recorded.

| input | rows | stations | result |
|---|---:|---:|---|
| `measurements-10m.txt` | 10,000,000 | 413 | byte-identical to `testdata/expected-10m.out` |
| `measurements-10k-stations-10m.txt` | 10,000,000 | 10,000 | byte-identical to `testdata/expected-10k-stations-10m.out` |
| `measurements-10k-stations-242m.txt` | 241,600,000 | 10,000 | byte-identical to `testdata/expected-10k-stations-242m.out` (gated through `CASES_EXTRA`, not in the default two-case gate; checked by E-33 when it timed that file) |
| **`measurements-1b.txt`** | **1,000,000,000** | **413** | **byte-identical to `testdata/expected-1b.out`**, added by this ticket |

The 1b reference output is new here and it is what closes the DoD's "byte-identical on the 1b file" line. It was produced by `code/gen`'s deliberately-slow reference (`cmd/reference`, `bufio.Scanner`, split on the first `;`, accumulate in integer tenths) in **38.42 s real / 37.48 s user**, single-threaded; the fast binary produced the same 10,664 bytes and 413 entries in **1.37 s real / 14.24 s user**. Both commands and the sha256 are in the data companion.

One consequence is worth naming because it changed a measurement in this same cycle: `experiment.sh:101-102` adds the timed file to the correctness gate whenever a reference output for it exists, so from this commit onward every 1b arm pays a 13.8 GB read before it is timed. E-36 is the control that prices it, and no 1b number taken after this commit is comparable to one taken before it without that control.

## Where the 233 ms is, with the profile behind it

`wall × 15 cores` is an identity, not a model, so the gap decomposes with no residual (E-32, three instrument rounds, reproduced to the digit).

- **80.53% user CPU, 7.99% system, 11.48% idle** at the headline; **76.02 / 6.86 / 17.12** in E-36's control bracket, where the whole of the difference is idle cores.
- **The 8.0 system points are closed, not open.** They are the kernel copying 13.8 GB out of the `F_NOCACHE` `pread` into the worker buffers, 9.49 GB/s [DERIVED]. It scales with bytes and not with call count, so no buffer size touches it, and the only mechanism that removes a copy is not copying: `-io mmap` is KILLED-on-numbers at **5.6× end-to-end** (16 KiB pages, 842k faults, `MADV_WILLNEED` making it worse).
- **The 11.5 idle points are open and their ceiling misses the target.** The mean number of workers with work is **12.85 of 20** against 15 cores; perfect packing is `(user + sys)/15` = **1.0740 s**, which is −11.48% of wall and still **+7.40% over target**. Parked as P-06.
- **There is no serial tail.** `wall − max(worker wall)` is −0.6 / +6.0 / −0.8 ms, inside `/usr/bin/time`'s resolution, and the cross-shard merge is instrumented at 2.28-3.16 ms.
- **The CPU profile, ranked by share of the non-syscall samples** (E-26's rule; pprof under-counts total CPU by ~25% here, so shares rank and its seconds price nothing): `indexDelimAt` **41.57-43.64%**, `parseTempWordFrom` 17.84-19.73%, `(*table).update` 12.52-14.05%, `runtime.memequal` 8.81-9.87%, `foldRowsPtr` flat 7.39-9.96%, per-row `merge` 1.92-2.59%.

**What 1.000 s would take, with both bounds and their assumptions named.** Holding today's overhead structure intact, user CPU must fall **17.58%**. Holding system CPU absolute and assuming P-06 delivers perfect packing with idle at zero, it must still fall **7.57%** [DERIVED; the assumption is a scheduling fix that gives up nothing, which nothing has demonstrated]. The only pot of that size is the separator scan at 41.57-43.64% of compute, and this study spent both of its mechanisms on it: queue item 1's fuse is CLOSED not-adopted at **+0.81%**, and item 2's batch tokenizer is KILLED at **+9.8% / +10.4%** despite a microbenchmark that promised −40.4%. **No third mechanism is invented here.**

## What was tried

36 ledger rows, each with a prediction written before the run. The full table is [`07-experiment-ledger.md`](07-experiment-ledger.md); the shape of it is that the verdict lines carry **KEEP 11 times and KILLED-on-numbers 8 times** (tokens, not rows: E-17 alone carries two verdicts, and the histogram's command is in `08-method-what-worked-data.txt` §1), and the rows that changed the study most were the ones that measured the method rather than the program: E-09 (a 10× smaller file ranks nothing: seven arms, seven disagreements), E-11 (a differently-shaped microbenchmark baseline is a fact about two other programs: a 50-point swing), E-16 (eight identical arms rank monotonically by 21.08%), E-18 versus E-23 (a derived slot correction produced six numbers, five of which re-measurement falsified), E-19 (recording a machine's state is not refusing to measure on it).

What actually moved the wall clock, in order: oversubscribing the cores to `NumCPU()*4/3` (−7.49%), the read buffer down to 1 MiB (−6.4% of wall with user CPU flat, bought parallel efficiency), folding the format check into the word the parse already loaded (−4.97% of wall from −6.33% of user CPU), and walking the fold with `unsafe.Add` with the name hash fused into the separator scan (E-27, the arm that took the compute floor under 1.000 s for the first time). What did not: mmap (5.6×), a shared cursor over segments, a split hash/entry table, batch tokenization in both SWAR and NEON, a bigger read buffer, a smaller table, the page cache, Go's runtime map at 413 stations (+52.20%), and double-buffered workers (+0.57% against a ceiling that had been re-derived four times).

## What is parked, with a test rather than a wish

`PARKED.md` carries **nine entries**, each with all seven fields and a concrete revive trigger. The four that came out of round 3 are P-06 (more readers than 20, ceiling 1.0740 s), P-07 (a vectorized table probe), P-08 (the merge and output at 10,000 stations) and P-09 (the quotiented bucket layout in the 10k regime). Two triggers were fired against the world rather than reasoned about during round 3's close, and one of them turned out half true: go1.27.0 does ship `simd`/`simd/archsimd` with arm64 support behind `GOEXPERIMENT=simd`, but the mask-to-general-register move that P-01 waits for is amd64-only.

## Reproducing any of it

```
cd 1brc/code/go && go build -o bin/1brc .
bash 1brc/scripts/check-correctness.sh                     # byte-compare, all committed cases
FILES=1b RUNS=5 WARMUP=1 bash 1brc/scripts/bench.sh label   # one configuration, no cooldown
bash 1brc/scripts/experiment.sh -i '<id>' -p '<prediction>' -a 'incumbent=' -a 'arm=<flags>'
```

`experiment.sh` refuses what this study learned produces uninterpretable numbers: it takes an exclusive measurement lock (status 3 if another is running), waits out a busy machine and refuses after 180 s (status 4), sleeps 20 s before every timed 1b run, names the incumbent at both ends, and rejects any file but 1b unless `--mechanism-only` is passed, in which case it stamps the output `NOT A VERDICT`. Every measurement file generated for this study is reproducible byte-for-byte from the seed and command in `02-baseline-data.txt`; none of them is in the repository.

## The honest paragraph

The binary reads 13.8 GB and aggregates a billion rows in **1.233 s**, 1.23× the target, at **79.6% parallel efficiency**, with the measured 754 ms read floor being 61.2% of its wall clock. Its compute floor is below the target and has been since E-27, so the target is not forbidden by CPU per row; it is missed on the read, in two forms, two-thirds of which is a kill with a number on it. The remaining open mechanism has a measured ceiling of 1.0740 s, which also misses. What would close the gap is a separator scan costing materially less than 41.6-43.6% of compute, and two attempts at that were measured and neither kept. Re-measured a day later the same binary reads 1.257 s, which the study publishes beside the headline rather than behind it. That is the result: **a measured miss, every number re-derivable from a recorded command, thirteen falsified claims in `CORRECTIONS.md`, nine ideas parked with tests, and no invented mechanism standing in for the part nobody solved.**
