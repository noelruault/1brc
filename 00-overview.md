# 1brc — the One Billion Row Challenge, on an Apple M5 Pro

**Question.** Can a Go program aggregate 1,000,000,000 weather measurements (13.8 GB) in under one second on this machine, and what does it take?

**Answer: no, by a measured margin.** The best measured wall clock is **1.233 s ± 0.010 s**, **+23.3% over the 1.000 s target**. Re-measured a day later on the byte-identical binary it reads **1.257 s** in a 0.000%-spread bracket, so the study publishes both figures; the conclusion is the same on either. The full statement, with the gap decomposed and the bottleneck profiled, is [`09-result.md`](09-result.md).

## Headline numbers

| | measured |
|---|---|
| 1,000,000,000 rows, shipped default | **1.233 s ± 0.010 s** (range 1.220-1.248, 5 runs, 20 s cooldown, bracket 0.072%), and **1.257 s** re-measured a day later |
| storage state | **uncached** (`F_NOCACHE`, 20 parallel 1 MiB `pread`s). Not "cold": `purge` needs sudo, so no cold number exists here |
| reader configuration | `-workers 20 -buf 1024 -split static -io pread -nocache=true -parse word -fold ptr -table combined -bits 17 -kernel row -fill off` |
| compute floor at perfect parallelism | **0.9812 s**, below the target since E-27 |
| measured read floor | **0.754 s**, 61.2% of the wall clock |
| parallel efficiency | **79.6%** (11.94 of 15 cores busy), at the 1.233 s figure |
| correctness | byte-identical to the reference on 10m (413 and 10,000 stations), 242m (10,000 stations) and **1b (413 stations)** |
| where it started | 2.607 s at 100m for the skeleton; 1.742 s at 1b for v1 |

**The gap decomposes with no residual**, because `wall × 15 cores` is an identity: **80.53% user CPU + 7.99% system + 11.48% idle**. The system points are the kernel copying 13.8 GB out of the `pread`, and the only mechanism that removes them (`mmap`) is killed at 5.6×. The idle points are open, and perfect packing would still land at 1.0740 s, +7.40% over target. The compute is 41.6-43.6% separator scan, and both mechanisms aimed at it were measured and neither kept.

## Method

Definition from the source, then a physical baseline, then kernel research, then an autoresearch optimization loop: **hypothesis queue → one experiment → one metric → keep/kill → ledger row → re-rank**. 36 ledger rows; **27 of them carry a prediction written before the run**, and the 9 that do not are the meta-results and instrument passes (E-09, E-11, E-18, E-19, E-20, E-21, E-22, E-26, E-32), which measure the study or the machine rather than an arm and so have nothing to predict. The rules the harness enforces, all of them bought with a measurement:

- A delta is taken only inside ONE bracketed hyperfine invocation, with the incumbent named **first and last**, 20 s of cooldown before every timed 1b run. Eight identical arms without that rank monotonically by **21.08%** (E-16).
- A bracket wider than 3% is a **refusal to quote any arm**, never a correction factor. A derived slot correction once produced six numbers and re-measurement falsified five of them (E-18 vs E-23).
- **Only the 1b file ranks arms.** Seven arms measured at 100m disagreed with 1b seven times (E-09). `experiment.sh` refuses any other file unless `--mechanism-only`, then stamps the output `NOT A VERDICT`.
- The harness **refuses**, it does not merely record: an exclusive measurement lock, and a quiet gate that waits a busy machine out and then refuses (E-19).
- Every published number names its **storage state** and its power source, and anything measured on battery is stamped PROVISIONAL.

## The record

| file | what it is |
|---|---|
| [`01-definition.md`](01-definition.md) | what 1BRC asks, written from the upstream repo at commit `db06419` with every rule cited |
| [`02-baseline.md`](02-baseline.md) | the physical floor: read bandwidth, the page cache losing to `F_NOCACHE`, generation, the file inventory |
| [`03-technique-recon.md`](03-technique-recon.md) | what the top entries do, and which of it survives on arm64 |
| [`04-asm-kernels.md`](04-asm-kernels.md) | SWAR, NEON and batch tokenization measured per row on this chip |
| [`05-go-techniques.md`](05-go-techniques.md) | how far raw Go goes: the table is the lever, `unsafe` is not |
| [`06-cross-disciplinary-transfer.md`](06-cross-disciplinary-transfer.md) | the seeding pass that fed the hypothesis queue |
| [`07-experiment-ledger.md`](07-experiment-ledger.md) | 36 rows, prediction before result, append-only |
| [`08-method-what-worked.md`](08-method-what-worked.md) | the method retrospective: what worked, what failed, what transfers |
| [`09-result.md`](09-result.md) | the closing report: headline, gap, bottleneck, what is parked |
| [`CORRECTIONS.md`](CORRECTIONS.md) | 13 claims this study published and later disproved, each corrected at every site |
| [`PARKED.md`](PARKED.md) | 9 ideas set aside, each with all seven fields and a concrete revive trigger |
| [`LICENSES.md`](LICENSES.md) | the licence of anything studied closely enough to matter |
| `code/go` | the contender. `code/gen` is the generator and the deliberately-slow reference; `code/asm` is the Plan 9 kernels |
| `scripts/` | `check-correctness.sh`, `bench.sh`, `experiment.sh` and the provenance library that gates them |
| `bench/` | every hyperfine invocation's raw output, dated, with its provenance header |

Every report has a `*-data.txt` companion holding the raw output **and** the command that produced it. No measurement file lives in this repository: all of them regenerate byte-for-byte from the seed and command in `02-baseline-data.txt`.

## Running it

From the repo root; both scripts build `1brc/code/go/bin/1brc` themselves.

```
bash 1brc/scripts/check-correctness.sh
FILES=1b RUNS=5 WARMUP=1 bash 1brc/scripts/bench.sh <label>
```

Machine of record: Apple M5 Pro, `hw.ncpu` 15, 24 GB, macOS 26.5.2 (Darwin kernel 25.5.0) arm64, go1.27.0. Leaderboard timings from the upstream repo are facts about a 32-core EPYC limited to 8 cores and are never compared against these as if the hardware were the same.

**This number is not a leaderboard entry, and the protocol differs on three axes** — say so before quoting it anywhere near an upstream time. Upstream pins to **8 cores** (`numactl --physcpubind=0-7`), serves the file from a **RAM disk** so I/O is excluded, and takes a **trimmed mean of 10 runs** (`evaluate.sh:211`, dropping fastest and slowest). This study runs on **15 unpinned cores**, reads from **SSD with `F_NOCACHE` so I/O is included and measured**, and takes 5 hyperfine runs behind a 20 s cooldown inside a bracketed invocation. The storage difference alone is not a detail: 754 ms of the 1.233 s is the read floor, and upstream's protocol would have deleted it. Correctness is a different matter and is directly comparable — `check-correctness.sh` gates on upstream's own twelve sample pairs (`CORRECTIONS.md` C14).
