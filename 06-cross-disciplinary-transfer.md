# 06 — Cross-disciplinary transfer

What other fields do with the same shape of work, and which of those mechanisms are worth transferring here. This is the repo's signature move (`quantization/01-cross-disciplinary-transfer.md`, `nearest-color-scaling/05-cross-disciplinary-transfer.md`): the point is not a literature survey, it is to leave the optimization rounds with a queue of testable hypotheses that a hunch would never have produced.

Every candidate below ends in exactly one of three states, per the ticket's rule that nothing is discarded without a number or a non-expiring argument: **QUEUED** (a hypothesis with a prediction, appended to `07-experiment-ledger.md`'s queue), **KILLED-on-argument** (an argument that does not expire, recorded so nobody re-derives it), or **PARKED** (not disproved, no number yet, with all seven `PARKED.md` fields).

Raw commands, machine facts and every piece of arithmetic: [`06-cross-disciplinary-transfer-data.txt`](06-cross-disciplinary-transfer-data.txt).

**Provenance.** Mechanism descriptions are written from prior reading of the cited work, not from primary text fetched in this session; each is marked `[unfetched]` and no NUMBER is taken from any of them. What was fetched this session is listed in the companion: the HTTP status of every URL cited, and two Crossref records. Every number in this report comes from this repo's own measurements.

## The unifying insight

**1BRC is a single-pass, fixed-schema, group-by aggregation over a 13.8 GB scan with 413 groups.** That sentence is a database person's description of the whole challenge, and it is why the database literature has more to say here than the "parsing" literature does. Written that way, the study's own components have standard names in other fields:

| this study | databases | bioinformatics | networking |
|---|---|---|---|
| the fold loop | a scan operator feeding a hash-aggregate | k-mer streaming | a packet-processing pipeline |
| `indexDelim` + `parseTempBranchless` | vectorized predicate evaluation | k-mer extraction | header parsing |
| `table.update` | hash-aggregate probe | k-mer count-table probe | flow-table lookup |
| 15 shards merged once | partial aggregation, per-thread, merged at the end | per-thread count tables | per-queue counters |
| `-workers` over static ranges | morsel-driven parallelism | block decomposition | RSS queues |

The last row is where the transfer is sharpest, and it is also where this study has already measured something the field would not predict: E-02 KILLED the shared-cursor split (the direct analogue of morsel dispatch) at 1b, 21% slower. That is a finding about THIS machine and THIS I/O path, and the interesting question is which half of the morsel idea it killed. See C1.

## What the machine is, and the one prior claim about it that nothing has measured

The two core classes are already on the record: `01-definition.md:91`, `02-baseline.md:15` and `03-technique-recon.md:25` all name 5 "Super" and 10 "Performance" cores. What is new here is the **L2 asymmetry** — 16 MiB for the Super cluster against 8 MiB for the Performance one (companion §1) — and, more usefully, that `02-baseline.md:15` left a hypothesis standing that nothing has tested: *"a uniform 15-way split is close to optimal on this chip"*, on the argument that both tiers are fast and neither is an E-core.

That hypothesis is what `reader.go` implements, and it has never been measured against the alternative. If the two classes differ in throughput at all, the equal split's wall clock is set by the slower class and the Super cores wait — a mechanism for part of the idle 25% that queue item 3 has so far only named.

The second fact from the same command matters for C3: a single shard's entry array is **6.00 MiB** (measured, companion §2), and fifteen of them are 90.0 MiB against 16 + 8 MiB of total L2. That number was published as 4 MiB at five sites, from an assumed 32-byte entry; it is 48 bytes on darwin/arm64. Corrected at every site, `CORRECTIONS.md` C4.

## C1 — Morsel-driven parallelism, and the half of it this study has not tested

**Mechanism** `[unfetched]`. HyPer's morsel-driven parallelism (Leis, Boncz, Kemper, Neumann, SIGMOD 2014, [morsels.pdf](https://db.in.tum.de/~leis/papers/morsels.pdf)) dispatches small fixed-size pieces of the input to a fixed worker pool at run time, so a worker that finishes early pulls the next piece. It solves two problems at once: load imbalance, and NUMA locality (a morsel is dispatched to the socket whose memory holds it).

**What this study already measured.** E-02 built the dispatch half — a shared atomic cursor over 2 MiB segments — and it lost by 21% at 1b. But that arm changed **two** things: it made the work dynamic, and it destroyed each worker's sequential read locality, because a worker's segments are no longer contiguous and `F_NOCACHE` reads have no readahead behind them. The 21% is attributed to the second change and nothing isolated the first.

**The transfer.** A static split *weighted by core class* is the **third** arm neither run tried: it keeps every worker's range contiguous, so it does not pay E-02's price, while removing the imbalance, so it collects morsel dispatch's actual benefit. E-02's own hypothesis line names the asymmetry as its motivation, so this is not a new idea about the machine — it is the untried way of acting on an idea the study has had since `03-technique-recon.md`.

**Prediction, derived** (companion §4): if the fast class runs at `r` times the slow class, proportional weighting is worth `(r/3 + 2/3)×` the equal-split wall clock — 3.3% at `r = 1.1`, 10.0% at `r = 1.3`, 16.7% at `r = 1.5`. `r` is unknown for this machine, so the experiment is two-step: measure `r` first (`-workers 5` against `-workers 10` at 1b tells us nothing directly, because I/O confounds it; a compute-bound single-worker probe over a resident buffer is the clean measurement), then run the weighted split end to end at 1b.

**Verdict: QUEUED as H-11.** Its honest risk is that the OS scheduler already migrates goroutines toward the fast cores and the effect is much smaller than the bound; that possibility is why the two-step measurement exists.

## C2 — Vectorized, batch-at-a-time execution

**Mechanism** `[unfetched]`. Column-store engines (MonetDB/X100, DuckDB, Velox) run every operator over a *vector* of ~1024 tuples rather than one tuple at a time. Kersten et al., VLDB 2018, [p2209-kersten.pdf](https://www.vldb.org/pvldb/vol11/p2209-kersten.pdf), is the paper that measures vectorization against compilation directly. The win is amortising per-call overhead, removing data-dependent branches from the inner loop, and letting the loop be SIMD-able at all.

**The transfer, part one: the tokenizer.** This is gigatoken's shape ([marcelroed/gigatoken](https://github.com/marcelroed/gigatoken)) arrived at independently, and `04-asm-kernels.md` already measured it as the biggest microbenchmark win in the study (−40.4% / −40.7% on the official key set). `go-v2-kernels` owns it and **measured it end to end in this same cycle**, so it is not queued here. **It lost**: both batch arms cost 9.8% and 10.4% more than the per-row scan at 1b, disjoint (E-10). The transfer of the *shape* is therefore falsified for this workload, which does not touch part two below — a vectorized probe is a different mechanism from a vectorized scan, and E-10 says nothing about it.

**The transfer, part two: the probe, which nobody has batched.** Vectorized engines do not stop at the scan. The hash-aggregate probe is vectorized too: compute the whole vector's hashes, issue the bucket loads for all of them, *then* resolve. That converts one serial chain of dependent, unpredictable loads into N independent misses that the memory system can overlap. `table.update` is still strictly per-row, and its load is exactly the dependent unpredictable kind.

**Prediction:** small at 413 stations, where only ~413 of 131,072 slots are ever hot and they will sit in L2 after the first megabyte; potentially large on a 10,000-station file, where the hot set is ~24× bigger. Concretely: **under 3% at 413, over 10% at 10k.** That second half needs a 1b 10k-station file (queue item 8), so this hypothesis inherits that dependency.

**Verdict: QUEUED as H-12**, blocked on the 1b 10k-station file for its interesting half.

## C3 — Bioinformatics: rolling hashes, minimizers, and the one idea that transfers

**Mechanisms** `[unfetched]`. (a) *Rolling / recursive hashing*: ntHash ([bcgsc/ntHash](https://github.com/bcgsc/ntHash); Mohamadi, Chu, Vandervalk, Birol, "ntHash: recursive nucleotide hashing", *Bioinformatics* 2016, [doi:10.1093/bioinformatics/btw397](https://doi.org/10.1093/bioinformatics/btw397), metadata confirmed via Crossref) computes a k-mer's hash from its predecessor's in O(1) instead of O(k). (b) *Minimizers*: Roberts, Hayes, Hunt, Mount, Yorke, "Reducing storage requirements for biological sequence comparison", *Bioinformatics* 2004, [doi:10.1093/bioinformatics/bth408](https://doi.org/10.1093/bioinformatics/bth408) — hash a window of k-mers, keep only the minimum, and most of the work disappears. (c) *Quotienting*: count-table implementations store only the part of the hash that the bucket index does not already determine, so an entry is smaller and the probed array is denser.

**(a) rolling hash — KILLED-on-argument.** A rolling hash pays off when the same bytes are hashed in many overlapping windows. Here each row contributes exactly one key, hashed once, from **one** 8-byte load (`hashWord`, and `05-go-techniques.md` measured that 8 bytes separate both key sets). There is no overlap to exploit and no second window; a recursive formulation would be strictly more arithmetic for the same answer. The argument does not expire because it rests on the problem's structure, not on a measurement.

**(b) minimizers — KILLED-on-argument.** Minimizers reduce work by *skipping* windows. Every row here must be counted, so nothing may be skipped. Same reason, same non-expiry.

**(c) quotienting — this is the one that transfers.** `entry` is 48 bytes (measured, companion §2) and 24 of those are a `[]byte` header for a name that is at most 100 bytes and is compared on almost every probe. Storing an inline 8-byte key prefix in the bucket and keeping the full name in a side array gives a 32-byte entry: `prefix uint64` + `min/max int32` + `sum int64` + `count int32` = 28, padded to 32. The probed array shrinks **1.5×**, and, more to the point, the hot compare becomes one word instead of a pointer chase into a separately-allocated key.

**Prediction:** this is a sharper version of what E-03 tested and lost. E-03 split the array in two and was **4.6% slower** at 413 stations, because it added a second array and a second cache line per hit. Quotienting adds no second array in the hit path, so it should not pay E-03's price: predict **0 to 4% faster at 413**, and more where the array stops fitting in cache. That E-03 lost in the same neighbourhood is exactly why this gets a number rather than an assumption.

**Verdict: QUEUED as H-13.**

## C4 — Compiler-style perfect hashing

**Mechanism** `[unfetched]`. `gperf` ([GNU manual](https://www.gnu.org/software/gperf/manual/gperf.html)) and CHD (Belazzougui, Botelho, Dietzfelbinger, ESA 2009, [esa09.pdf](https://cmph.sourceforge.net/papers/esa09.pdf)) build a collision-free hash for a key set known in advance, so a lookup is one load with no probing and, for a *minimal* perfect hash, no wasted slots.

**The transfer.** The station set is not known in advance and the challenge forbids assuming it — but it is *discoverable*: 413 stations appear within the first few tens of thousands of rows. A two-phase table (learn the key set from the first buffer, build a perfect hash, run the rest through it with a fallback for unseen keys) does not hardcode anything; it learns, which is a different thing.

**Prediction, and why it is PARKED rather than queued.** The thing perfect hashing removes is *probe collisions and the key compare*. With 413 keys in 131,072 buckets the load factor is 0.3%, so almost every probe already hits its first slot, and the compare that follows is perfectly predicted. Predict **under 2% at 413 stations** — and the cost is a second code path, a fallback, and a per-worker build. Nothing yet shows the probe is hot enough to be worth that.

**Verdict: PARKED (P-03).** **Revive when** the profile from queue item 3 attributes a measurable share of cycles to `table.update`'s probe, or when a 1b 10,000-station file makes the load factor 7.6% instead of 0.3%. Cost to revive: about a day; the build is 413 keys, the risk is entirely in the fallback path's correctness.

## C5 — Poll-mode and batched I/O: the wrong half and the right half

**Mechanisms** `[unfetched]`. DPDK's poll-mode drivers ([programmer's guide](https://doc.dpdk.org/guides/prog_guide/)) take the kernel and its interrupts out of the data path entirely. `io_uring` ([io_uring(7)](https://man7.org/linux/man-pages/man7/io_uring.7.html), [axboe/liburing](https://github.com/axboe/liburing)) batches submissions and completions through shared rings so N I/Os cost one syscall, or none.

**The wrong half: syscall count — KILLED-on-argument, with the number.** v1 issues one `pread` per 4 MiB buffer: 13,795,610,267 bytes / 4 MiB is about **3,289 reads for the whole file**, roughly 220 per worker. Even at 10 µs each that is ~33 ms of syscall entry spread across 15 cores against a 754 ms I/O floor. There is no room in that budget for batching to matter, and darwin has no `io_uring` to batch with anyway. The argument does not expire: it is arithmetic over a recorded byte count and a recorded buffer size, and shrinking the syscall count can only ever recover a fraction of a number that is already small.

**The right half: overlapping I/O with compute.** DPDK's actual structural lesson is not "fewer syscalls", it is that the interface keeps filling buffers while the core processes the previous ones. `foldRange` does the opposite, *by construction*: it calls `f.ReadAt`, waits, then calls `t.fold` on what arrived, in the same goroutine, alternating. That is a fact about the code (`reader.go`, the `for off < readEnd` loop), not an inference from a timing. Double-buffering each worker — a reader goroutine filling buffer B while the fold runs on buffer A — makes the overlap explicit instead of leaving it to the OS to cover one worker's read with another worker's compute.

**Prediction, with its assumption stated.** The ledger measures 19.657 s of user CPU against 1.742 s of wall clock, which is 11.28 of 15 cores busy. If explicit double buffering closed the *whole* idle gap and nothing else changed, the same CPU work spread over 15 busy cores would take 19.657 / 15 = **1.310 s**. That is a **ceiling**, not a prediction: it assumes every idle core is idle because of read stalls, which is precisely what has not been measured. Predict something between 5% and that ceiling's 25%.

**Superseded by measurement, and not a correction: the assumption held and the number moved because the binary got faster.** E-20 measured the idle gap with `-phases` and it *is* the read (29-30% of worker wall blocked in `pread`, against shard skew under 4% and a merge under 0.4%), so the assumption this paragraph flagged as unmeasured is now measured and stands. The ceiling itself is recomputed on the current baseline — 18.53 s of CPU over 15 cores is 1.235 s against a 1.50 s wall — which is **17.6%, not 25%**, because E-17's oversubscription already took some of the gap this figure was reserving. The ledger's queue item 14 and its board row carry the measured number; this paragraph keeps the original so the arithmetic that produced 25% stays readable.

**A trap to name, because this report nearly walked into it.** The ledger's "0.988 s of compute" is *defined* as 1.742 − 0.754. Adding it back to the floor to conclude "the sum is the wall clock, therefore nothing overlaps" is circular, and it is the kind of arithmetic the last review gate caught twice. The claim above rests on the shape of the loop instead.

**Verdict: QUEUED as H-14**, and it is the largest single-mechanism candidate in this report.

## C6 — GPU offload via Metal

**Mechanism** `[unfetched]`. Apple silicon has unified memory ([Metal device and work submission](https://developer.apple.com/documentation/metal/gpu_devices_and_work_submission), [MSL specification](https://developer.apple.com/metal/Metal-Shading-Language-Specification.pdf)), so there is no PCIe copy: a buffer the CPU wrote is a buffer the GPU can read. This machine reports **16 GPU cores** (companion §1). Tokenizing and aggregating are both embarrassingly parallel over byte ranges, and per-station reduction is a standard atomic-into-threadgroup-memory pattern.

**Why it cannot be killed.** The file still has to come off disk, and `02-baseline.md` measured that floor at **754.4 ms ± 8.8 ms**. A GPU cannot lower it. But the target is 1.000 s, so a perfect offload — floor plus launch plus reduction — would land *under target*. An idea whose ceiling clears the goal does not get killed on an argument.

**Why it is parked anyway.** Two reasons, and only the first expires. (1) `spec.md:5` asks for **a Go implementation**; a Metal kernel driven through cgo is a different artifact, and it would be a measured comparison, not the answer. (2) The cost is a fork of the study: a Metal kernel, a cgo boundary, a reduction across 16 GPU cores, and the whole correctness gate re-established on the new path.

**Verdict: PARKED (P-04).** **Revive when** the Go path has been measured to within ~15% of the 754 ms floor and is still over target — at that point the remaining gap is compute, which is the only gap a GPU can close. Cost to revive: several days.

## What this adds to the ledger

Four hypotheses queued (**H-11** heterogeneity-weighted split, **H-12** vectorized probe, **H-13** quotiented entry, **H-14** double-buffered I/O), two killed on non-expiring arguments (rolling hashes, minimizers) plus one killed with its number (syscall batching), and two parked with all seven fields (**P-03** perfect hashing, **P-04** Metal offload). The queue section of `07-experiment-ledger.md` carries them; `PARKED.md` carries P-03 and P-04.

The ranking that falls out, by the size of the gap each could close:

| | candidate | mechanism it borrows | predicted | blocked on |
|---|---|---|---|---|
| 1 | **H-14** double-buffered workers | DPDK's fill-while-you-process | 5% to a 25% ceiling [**re-derived on the current baseline: 17.6%, E-20**] | nothing |
| 2 | **H-11** core-class-weighted split | morsel-driven parallelism, minus the part E-02 killed | 3-17%, `r` unknown | measuring `r` |
| 3 | **H-13** quotiented 32-byte entry | k-mer count tables | 0-4% at 413, more at 10k | nothing |
| 4 | **H-12** vectorized probe | vectorized query execution | <3% at 413, >10% at 10k | a 1b 10k file for the half that matters |

The honest summary of this pass: the biggest thing it found is not a kernel, it is that **the reader alternates instead of overlapping**, and the second biggest is that **`02-baseline.md`'s "a uniform 15-way split is close to optimal" is an unmeasured hypothesis the code has been treating as a decision**. Both were sitting in the existing measurements; it took borrowing someone else's vocabulary to see them.
