# Parked ideas — 1brc

Convention and required fields: [`../PARKED.md`](../PARKED.md). **Re-read this file whenever any baseline in this study moves** — that is exactly when a parked entry can silently become valuable and exactly when nobody thinks to look.

Live state and the active hypothesis queue are in `07-experiment-ledger.md` (not yet written; `go-v1-parallel` creates it). This file is only for things *not* being worked on.

The baseline every entry below was measured against, so a future reader can see when the comparison goes stale: **staged SWAR at 11.225 ns/row on the 413-station corpus and 13.760 on the 10k long-name corpus**, both PROVISIONAL (battery), both from `04-asm-kernels-data.txt`.

---

## P-01 — Per-row NEON separator scan · `parked`

**What it is.** Find `;` and `\n` with a 16-byte NEON compare per row — `VLD1` the window, `CMEQ` against the needle, `SHRN #4` to pack the byte lanes into a nibble mask, `VMOV` the mask into a general register, count trailing zeros. Built and correct: `neon_arm64.s`, driven by `TokenizeStagedNEON`, which differs from `TokenizeStagedSWAR` in exactly one line.

**Why parked — with the number.** It loses to 8-byte SWAR by **+1.830 ns/row (+16.3%)** on the 413 corpus and by **+3.185 ns/row (+23.1%)** on the 10k corpus, against the baseline above. The cause is measured rather than argued: `neonTransferProbe` isolates one vector-to-general round trip at **1.080 ns/row** (413) and **0.825** (10k), and that transfer sits in the loop's critical path because the loop branches on it. arm64 has no `PMOVMSKB` and no branch-on-vector instruction to avoid it with. The margin *grows* on the corpus where the wider window should help most, which says the transfer is a per-window cost in the dependency chain, not a startup cost being amortised.

**Depends on.** (a) One transfer per row — the entire loss is a per-row transfer that a batched loop would pay once per ~2.3 rows. (b) The 1.080 ns/row transfer figure, which is transfer-*plus*-non-inlinable-call and has never been separated (Go has no intrinsic for the narrow-and-move). (c) The official key set's 8.0-byte mean name length, which is what keeps the 8-byte SWAR window sufficient 65.9% of the time. (d) Go's inlining of the SWAR path, which the assembly call cannot have.

**Revive when.** Any of: a Go release exposes the vector-to-general move as an intrinsic, or `//go:noescape` assembly becomes inlinable, either of which deletes the call half of the measured cost; or a kernel is needed for an input whose names average over ~32 bytes, where the window advantage is largest; or the batch shape (`TokenizeBatch`) is rejected end-to-end in `go-v1-parallel` and a per-row scan is the shape being optimised again.

> **This last trigger FIRED, the entry was re-read, and it stays parked** (`go-v2-kernels`, `07-experiment-ledger.md` E-10). The batch shape lost end to end by 9.8-10.4% at 1b and the per-row scan IS the shape being optimised again — but P-01's own number is a comparison *between* per-row scans, and per-row NEON lost to per-row SWAR by 16.3-23.1%. The trigger fired into an entry whose number still says no. Firing a trigger is not the same as reviving, and recording the difference is the point of the register.

**Cost to revive.** Low. The kernel exists, is correct, is mutation-tested and has a page-guard test; reviving is re-running the benchmark, not rebuilding.

**Where.** `1brc/code/asm/neon_arm64.s`, `neon_arm64.go`, `TokenizeStagedNEON` in `tokenize.go`; benchmarks `BenchmarkTokenize/staged-neon` and `BenchmarkNEONTransferFloor`; numbers in `04-asm-kernels-data.txt`; commits `20aa0d3`, `e29141f`, `a4a49df`.

> **Not killed.** The mechanism is not wrong; it is priced wrong at one transfer per row. Its own rescue lived in the batch kernel, which pays the same transfer once per window — and E-10 has now measured that rescue losing 10.4% end to end at 1b, so P-01 has one fewer route back than it had when it was written.

---

## P-02 — NEON temperature parse · `parked`

**What it is.** Parse the fixed-point temperature field with a vector compare instead of merykitty's SWAR-style branchless arithmetic — the third variant `03-technique-recon.md` named as H3's test, alongside the scalar digit loop and the branchless parse. **Never built.**

**Why parked — with the number.** Parked on a measured bound rather than on a measurement of the thing itself, and that distinction is the point of this entry. Any per-row NEON kernel pays **1.080 ns/row** (413) before doing useful work, measured bare by `neonTransferProbe`. The *entire* spread between the two parses that were built is **1.735 ns/row** (413, scalar wins) and **3.295** (10k, branchless wins). So a NEON parse would have to beat the better of the two existing parses by more than the whole measured spread between them just to break even on its own transfer.

**Depends on.** (a) The 1.080 ns/row per-row transfer floor — the same figure P-01 depends on, so both entries go stale together. (b) The parse staying a per-row operation; the bound assumes one transfer per row and says nothing about a parse inside a batched loop. (c) The 1.735/3.295 ns/row spread between the scalar and branchless parses, which is itself input-dependent and was measured with the separator scan held fixed at SWAR.

**Revive when.** ~~The batch shape wins end-to-end in `go-v1-parallel`.~~ **This trigger is now SPENT: `go-v2-kernels` measured both batch arms losing at 1b by 9.8% and 10.4%, disjoint (`07-experiment-ledger.md` E-10), so the batched loop this entry was waiting for is not the loop the binary runs.** What remains is either P-01 trigger (a) — an intrinsic or an inlinable call, which moves the floor for both entries — or an input whose names average over ~32 bytes, where E-10's revive trigger and this one coincide. Without one of those, the 1.080 ns/row per-row transfer bound stands and this entry moves toward `killed`.

**Cost to revive.** Moderate. Unlike P-01 nothing exists: a kernel, a scalar-reference correctness check over all 1999 legal temperatures at every alignment, and a benchmark. Perhaps a day.

**Where.** Nothing built. The argument and its numbers are `04-asm-kernels.md` H3; the transfer floor is `BenchmarkNEONTransferFloor` in `1brc/code/asm/bench_test.go`; the hypothesis as originally stated is H3 in `03-technique-recon.md`.

> **This is a deviation from the recon's stated test for H3, recorded as one.** H3 named three variants and two were built. The third is parked on a number, which is the bar `spec.md:37` sets for not building something — but it remains a bound, not a measurement, and the entry says so.

---

## P-03 — Learned perfect hashing for the station table · `parked`

**What it is.** Replace the open-addressing probe with a perfect hash built at run time: learn the key set from the first buffer a worker reads, build a collision-free (ideally minimal) hash over those keys, then run the remaining rows through a table lookup with no probing and no key compare, keeping a fallback path for a key the sample missed. The construction is the compiler world's — `gperf` and CHD (Belazzougui, Botelho, Dietzfelbinger, ESA 2009) — applied to a key set that is discovered rather than declared, so nothing about the station list is hardcoded and the challenge's rules are not bent.

**Why parked — with the number.** It optimizes a step nothing has shown to be hot. 413 stations in 131,072 buckets is a **0.3% load factor**, so almost every probe resolves in its first slot and the `bytes.Equal` that follows is perfectly predicted. **Predicted under 2%** at 413 stations, against a two-phase table, a fallback path and a per-worker build. `05-go-techniques.md` measured the ADJACENT thing — a prefix-hashed open-addressing table beats Go's map by 15.8% on the 413 set — so the probe is already the cheap part of the table.

**Depends on.** (a) The 0.3% load factor, which is `-bits 17` (E-07's KEEP) against 413 stations; a 10,000-station file makes it 7.6% and a smaller table makes it worse still. (b) Nothing having attributed measurable time to `table.update`'s probe — queue item 3's profile has not been run. (c) The 413-station key set being what the headline is measured on.

**Revive when.** Either: the profile from queue item 3 attributes a measurable share of cycles to the probe or to `bytes.Equal`; or a 1b 10,000-station file (queue item 8) exists and the probe is measurably hot on it; or H-13 (the quotiented entry) wins, which would show the bucket layout is worth spending on at all.

**Cost to revive.** Moderate, about a day. The build is only 413 keys, so the construction is not the risk; the fallback path is, because it is the branch that never runs on clean data and therefore the branch no byte-compare will ever exercise — the same shape as the three first-vs-last-`;` bugs this study has already shipped.

**Where.** Nothing built. The argument is `06-cross-disciplinary-transfer.md` C4; the table it would replace is `1brc/code/go/table.go`; the load-factor and hash numbers are `05-go-techniques.md` and `07-experiment-ledger.md` E-07.

---

## P-04 — Metal GPU offload · `parked`

**What it is.** Tokenize and aggregate on the GPU. Apple silicon's unified memory means a buffer the CPU filled is one the GPU reads with no copy; this machine reports 16 GPU cores. Byte ranges are embarrassingly parallel and per-station reduction is the standard atomics-into-threadgroup-memory pattern. The CPU would do the reading and the GPU the folding.

**Why parked — NOT killed, and the number is what stops it being killed.** The read floor is **754.4 ms ± 8.8 ms** (`02-baseline.md`), and no accelerator lowers it. But the target is **1.000 s**, so a perfect offload — floor plus launch plus reduction — lands *under target*. An idea whose ceiling clears the goal cannot be killed on numbers. What parks it is scope: `spec.md:5` asks for a Go implementation, so a Metal kernel behind cgo is a measured comparison and not the answer, and standing it up means a new kernel, a cgo boundary, a cross-core reduction and the whole correctness gate re-established on a second path.

**Depends on.** (a) The 754.4 ms floor staying the floor — if a faster read path is found, the GPU's ceiling drops with it and the case gets *stronger*, not weaker. (b) `spec.md:5`'s "a Go implementation", which is a scope constraint the operator can change and is the only expiring half of this entry. (c) The Go path still being over target; if Go reaches 1.0 s there is nothing left for a GPU to win.

**Revive when.** The Go path is measured within ~15% of the 754 ms floor (roughly 870 ms) and is still over target — at that point every remaining millisecond is compute, which is the only thing a GPU can attack. Or the operator widens `spec.md:5` to allow a non-Go comparison arm.

**Cost to revive.** High, several days: a Metal compute kernel in MSL, a cgo bridge, a reduction across 16 GPU cores, and the correctness gate re-derived on the new path. This is a fork of the study, not a round of it.

**Where.** Nothing built. The argument is `06-cross-disciplinary-transfer.md` C6; the floor it is measured against is `02-baseline.md` / `02-baseline-data.txt`; the GPU core count is `06-cross-disciplinary-transfer-data.txt` §1.

## P-05 — H-11, the core-class-weighted split · `subsumed`

**What it is.** Split the file by byte range in proportion to core throughput rather than equally: 5 "Super" cores with 16 MiB of L2 and 10 "Performance" cores with 8 MiB (`06-cross-disciplinary-transfer-data.txt` §1) get shares weighted by a measured ratio `r`, instead of `reader.go` handing every worker the same number of bytes. Borrowed from morsel-driven parallelism, minus the dynamic dispatch E-02 already killed here.

**Why subsumed — and the number, with the baseline it was measured against.** The mechanism is finish-time imbalance: an equal split makes the fast cores idle while the slow ones finish, and weighting reclaims that tail. E-20 measured the tail directly with `-phases` on the current default (20 workers, 4 MiB buffers, `F_NOCACHE`, static split, 1b rows, AC power, load 4.20): **worker wall spread is 1.029-1.038 max/min over three runs**, so there are at most **3.8%** of imbalance to collect, against a prediction of 3.3% to 16.7% as a function of `r`. E-17's oversubscription to `NumCPU()*4/3` = 20 workers is what collects it: with more goroutines than cores the scheduler refills a core that finishes early, which is the same balancing a weighted split would buy by arithmetic. The prediction was not wrong about the mechanism, it was written against a shape the binary no longer has.

**Depends on.** The default staying oversubscribed. The 3.8% ceiling is a property of 20 workers on 15 cores, not of the split; at one worker per core the imbalance E-20 cannot see would be back, and `r` would matter again.

**Revive when.** The default returns to one worker per core (E-17's plateau is re-measured and lost, or a future arm pins workers to cores for cache-locality reasons), OR a `-phases` run on some future configuration shows worker wall spread above ~1.15. Either restores the tail the weighting exists to take.

**Cost to revive.** Low, under an hour: `r` on a resident buffer, then a weighted `staticRanges` behind a flag. The arithmetic is already written in the ledger's item 11, and the boundary-trap tests the range splitter already carries (`TestARangeShorterThanARowOwnsNothing` and its siblings) cover a weighted split unchanged, because they sweep worker counts rather than assuming equal shares.

**Where.** Nothing built. The hypothesis is `06-cross-disciplinary-transfer.md` H-11 and the ledger's queue item 11; the measurement that subsumed it is E-20 / [`bench/2026-09-01T192513Z-queue-item-3-where-the-idle-cores-go.txt`](bench/2026-09-01T192513Z-queue-item-3-where-the-idle-cores-go.txt); the claim it finally tested is `02-baseline.md:15`'s "a uniform 15-way split is close to optimal on this chip", which is now measured and true at 20 workers.
