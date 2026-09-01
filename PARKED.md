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

**Cost to revive.** Low. The kernel exists, is correct, is mutation-tested and has a page-guard test; reviving is re-running the benchmark, not rebuilding.

**Where.** `1brc/code/asm/neon_arm64.s`, `neon_arm64.go`, `TokenizeStagedNEON` in `tokenize.go`; benchmarks `BenchmarkTokenize/staged-neon` and `BenchmarkNEONTransferFloor`; numbers in `04-asm-kernels-data.txt`; commits `20aa0d3`, `e29141f`, `a4a49df`.

> **Not killed.** The mechanism is not wrong; it is priced wrong at one transfer per row. Its own rescue is already measured and lives in the batch kernel, which pays the same transfer once per window instead.

---

## P-02 — NEON temperature parse · `parked`

**What it is.** Parse the fixed-point temperature field with a vector compare instead of merykitty's SWAR-style branchless arithmetic — the third variant `03-technique-recon.md` named as H3's test, alongside the scalar digit loop and the branchless parse. **Never built.**

**Why parked — with the number.** Parked on a measured bound rather than on a measurement of the thing itself, and that distinction is the point of this entry. Any per-row NEON kernel pays **1.080 ns/row** (413) before doing useful work, measured bare by `neonTransferProbe`. The *entire* spread between the two parses that were built is **1.735 ns/row** (413, scalar wins) and **3.295** (10k, branchless wins). So a NEON parse would have to beat the better of the two existing parses by more than the whole measured spread between them just to break even on its own transfer.

**Depends on.** (a) The 1.080 ns/row per-row transfer floor — the same figure P-01 depends on, so both entries go stale together. (b) The parse staying a per-row operation; the bound assumes one transfer per row and says nothing about a parse inside a batched loop. (c) The 1.735/3.295 ns/row spread between the scalar and branchless parses, which is itself input-dependent and was measured with the separator scan held fixed at SWAR.

**Revive when.** The batch shape wins end-to-end in `go-v1-parallel`. A parse inside a batched loop amortises the same transfer over ~2.3 rows, at which point the bound above no longer applies and H3 has a third variant worth building. Also revive on either P-01 trigger (a) — an intrinsic or an inlinable call moves the floor for both.

**Cost to revive.** Moderate. Unlike P-01 nothing exists: a kernel, a scalar-reference correctness check over all 1999 legal temperatures at every alignment, and a benchmark. Perhaps a day.

**Where.** Nothing built. The argument and its numbers are `04-asm-kernels.md` H3; the transfer floor is `BenchmarkNEONTransferFloor` in `1brc/code/asm/bench_test.go`; the hypothesis as originally stated is H3 in `03-technique-recon.md`.

> **This is a deviation from the recon's stated test for H3, recorded as one.** H3 named three variants and two were built. The third is parked on a number, which is the bar `spec.md:37` sets for not building something — but it remains a bound, not a measurement, and the entry says so.
