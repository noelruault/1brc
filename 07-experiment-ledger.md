# 07 — Experiment ledger

Append-only. One row per experiment, each with the idea, the prediction made BEFORE the run, the measured result, and a verdict. A row is never edited once its verdict is written; a superseded number gets a new row and a `CORRECTIONS.md` entry. Raw output and every command: [`07-experiment-ledger-data.txt`](07-experiment-ledger-data.txt).

**Verdict vocabulary** (`PARKED.md`'s, plus the two that keep code): `KEEP` (the default now), `KILLED-on-numbers` (records the number AND the baseline it was measured against, because numbers move), `PARKED` (not disproved, no number yet), `SPLIT` (the answer depends on the input and both arms stay).

**The measurement rule this ledger enforces**: a delta is only ever taken between two arms of the SAME hyperfine invocation, because the identical binary measured 1.667 s in one invocation and 1.717 s in another. Cross-invocation numbers are recorded and never subtracted.

## Where v1 stands

| | measured | source |
|---|---|---|
| 1,000,000,000 rows | **1.742 s ± 0.019 s** (range 1.712-1.760, 5 runs) | [`bench/2026-09-01T152951Z-v1-parallel.txt`](bench/2026-09-01T152951Z-v1-parallel.txt) |
| 100,000,000 rows | 161.0 ms ± 4.8 ms | same |
| 10,000,000 rows | 27.8 ms ± 0.9 ms | same |
| the skeleton it replaces | 2.607 s at 100m and 260.5 ms at 10m, 26.1 ns/row | `bench/2026-09-01T125649Z-skeleton.txt` |
| target | 1.000 s | `spec.md:5` |

So v1 is **1.74× over target** and **16.2× faster than the skeleton at 100m** (2.607 s against 161.0 ms; 9.4× at 10m). PROVISIONAL: battery. Correctness gate green on both 10m files (413 and 10,000 stations) before every number above.

Derived, from the same run: 19.657 s of user CPU for 1.742 s of wall clock is **11.3 cores of 15 busy, 75% parallel efficiency**, and 19.7 ns of CPU per row. The I/O floor measured in `02-baseline.md` is 754 ms, so **43% of the current wall clock is the unavoidable read** and the compute above it is what the remaining rounds have to attack.

## Rows

### E-01 — 8 bytes of station name are enough to hash on (H4)

- **Idea:** hash the first 8 bytes of the name, masked at the separator, and let a full `bytes.Equal` resolve collisions.
- **Prediction** (`03-technique-recon.md`, H4): zero unresolved collisions on the 413 set, manageable on the 10k set.
- **Measured** (`05-go-techniques.md`, a static computation over both key sets, no benchmark): 412 distinct 8-byte prefixes for 413 names — one collision, `Alexandra`/`Alexandria` — and 413 distinct 16-byte prefixes. 10,000 distinct 8-byte prefixes for the 10k set.
- **Verdict: KEEP.** Shipped as `kernel.go:hashWord`. The one collision costs one extra probe on one station.

### E-02 — a shared cursor over 2 MiB segments beats a static equal split (H1)

- **Idea:** hand each worker 2 MiB at a time from an atomic cursor, so the Performance/Efficiency core asymmetry cannot leave the static split's slowest shard setting the wall clock.
- **Prediction** (H1): dynamic wins by **≥5%**; the static split's last shard idles the fast cores.
- **Measured, invocation B (1b):** static **1.667 s** [1.643-1.685], cursor **2.017 s** [1.976-2.058]. Cursor is **21.0% SLOWER**, disjoint over five runs each. At 100m (invocation A) the two are indistinguishable: 152.9 ms [150.9-155.5] against 153.6 ms [152.3-154.3], overlapping ranges.
- **Verdict: KILLED-on-numbers** against static-split-at-1b = 1.667 s in invocation B. The prediction was not just wrong in size, it was wrong in direction. The mechanism is not established; the measured symptom is that cursor mode does 21% more wall clock for the same 19 s of user CPU, which points at 15 workers re-entering the same file at unpredictable offsets instead of streaming one contiguous range each. **Revive trigger:** a run where the static split's shards demonstrably finish more than 5% apart, which nothing here measures yet.

### E-03 — splitting the hash array from the entry array (H5)

- **Idea:** probe an array of 8-byte hashes instead of 48-byte entries [CORRECTED, C4: published as 32-byte], so a miss touches a sixth as much memory [CORRECTED, C4: published as "a quarter"].
- **Prediction** (H5): **≥10%** better on the 10k-station file, **≈0%** on the 413 one. `05-go-techniques.md` sharpened the mechanism: at 1<<17 buckets the entry array is 6.00 MiB touched at random [CORRECTED, C4: published as 4 MiB] and the hash array is 1 MiB.
- **Measured, invocation B (1b, 413 stations):** combined **1.667 s** [1.643-1.685], split **1.743 s** [1.726-1.769]. Split is **4.6% slower**, disjoint. At 100m the ranges overlap.
- **Verdict: SPLIT — the 413 half is KILLED-on-numbers** (against 1.667 s), **the 10k half is PARKED and untested**, because there is no 1-billion-row 10,000-station file: the 10k stressor is a 10m file, where nothing is I/O bound and no wall-clock verdict is meaningful. The flag stays (`-table split`). **Revive trigger:** generate a 1b 10k-station file (the generator does 725 MB/s, so ~20 s) and re-run; that is also the only condition under which `05-go-techniques.md`'s working-set hypothesis can be tested end to end.

### E-04 — mmap the whole file (H7)

- **Idea:** the universal choice in the public 1BRC corpus — map 13.8 GB and let the page-fault path do the reading.
- **Prediction** (H7, written after `02-baseline.md` measured mmap losing 5-9× on a *scan*): parallel `pread` stays ahead once aggregation runs too, and `madvise(MADV_WILLNEED)` is the rescue that might change it.
- **Measured, invocation C (1b, own anchor):** anchor `pread` **1.717 s** [1.687-1.759], mmap **9.693 s** [9.619-9.827], mmap **+ `MADV_WILLNEED` 10.782 s** [10.702-10.893]. mmap is **5.6× slower** and the named rescue makes it **11% worse still**. System time tells the story: 18.4 s for mmap against 2.3 s for `pread`. At 100m — a 1.4 GB file that fits in RAM — mmap is *marginally faster* than `pread` (151.2 ms against 152.9 ms, overlapping) and `madvise` is already 18% worse than the default arm (19.6% worse than mmap without it).
- **Verdict: KILLED-on-numbers** against `pread`-at-1b = 1.717 s in invocation C. This is the largest divergence between the public corpus's design and ours, and it is now measured end-to-end rather than on a scan, which is what `spec.md:37` required before anything could be discarded. macOS's 16 KiB pages mean ~842k faults on a path that does not parallelise. **Revive trigger:** a machine with a different page size or a `MAP_POPULATE`-equivalent; `madvise` was the rescue and it is spent.

### E-05 — the branchless temperature parse against a branchy scalar one (H3)

- **Idea:** merykitty's magic-multiply parse, no data-dependent branch.
- **Prediction** (`04-asm-kernels.md`, H3 SPLIT): the winner is set by branch predictability, so only the real 15-shard loop settles it. The microbenchmark had the branchless parse **15.2% SLOWER** than scalar on the 413 corpus.
- **Measured, invocation B (1b):** branchless **1.667 s** [1.643-1.685], scalar **1.857 s** [1.760-2.202]. The scalar arm costs **11.4% more** than the branchless default it replaces, and the scalar arm is by far the noisiest in the invocation (σ 0.193 s against 0.017 s). At 100m the ordering **inverts**: scalar 146.4 ms [143.5-149.7] beats branchless 152.9 ms [150.9-155.5], disjoint.
- **Verdict: KEEP branchless as the default, and the FLAG STAYS.** H3's "it depends on the input" is confirmed and sharpened: it depends on the *scale* too, on the same input. The microbenchmark ranked it backwards, and so did the 100m end-to-end run. Note the branchless arm carries a validation tax the scalar arm does not (`kernel.go:validTemp`, six byte compares, needed because the branchless parse cannot reject a malformed field), so the 11.4% is measured *including* that handicap.

### E-06 — per-worker read buffer: 1 MiB against 4 MiB

- **Idea:** `automataIA/1brc-rs` uses one reusable 4 MiB buffer per worker; `02-baseline.md` only swept 1 MiB against 8 MiB, and benhoyt's Go entries use 1 MiB.
- **Prediction:** none recorded before the run — this was a sweep, not a hypothesis, and it is labelled as such.
- **Measured:** at 1b (invocation B) 4 MiB **1.667 s** beats 1 MiB **1.713 s** [1.700-1.720], disjoint, **2.8%**. At 100m (invocation A) 1 MiB **143.5 ms** [139.8-147.1] beats 4 MiB **152.9 ms**, disjoint, **6.1%** the other way.
- **Verdict: KEEP 4 MiB** (the 1b answer is the one that counts). 8 MiB and 16 MiB are untried at 1b.

### E-07 — table size: 1<<14 against 1<<17 buckets

- **Idea:** `05-go-techniques.md` measured load factor mattering (8.5% on the 10k key set) and working-set size plausibly mattering the other way.
- **Prediction:** 1<<14 might win on the 413 set, where 413 entries make load factor irrelevant and a 768 KiB array beats a 6.00 MiB one [CORRECTED, C4: published as 512 KiB against 4 MiB].
- **Measured, invocation B (1b):** 1<<17 **1.667 s**, 1<<14 **1.787 s** [1.765-1.812]. The bigger table is **7.2% faster**, disjoint — against the prediction. At 100m the small table wins by 7.1%, disjoint. Another arm whose direction inverts with scale.
- **Verdict: KEEP 1<<17.** The mechanism is not established. Hypothesis for a later round: with 15 shards live, more buckets means fewer probe collisions in *aggregate*, and the entry array's size stops mattering because only ~413 lines of it are ever hot.

### E-08 — `F_NOCACHE` against the page cache, end-to-end

- **Idea:** `02-baseline.md` measured 15 uncached preads reading 13.8 GB in 754 ms against 1.126 s page-cached, because the file is 53.5% of RAM.
- **Prediction:** the ordering survives once aggregation competes for the same bandwidth.
- **Measured, invocation B (1b):** `F_NOCACHE` **1.667 s**, page-cached **1.759 s** [1.720-1.787], disjoint, **5.5%**. At 100m — 1.4 GB, comfortably resident — the page cache wins by 3.5%, disjoint.
- **Verdict: KEEP `F_NOCACHE`.** `02-baseline.md`'s ordering is confirmed end-to-end at 1b and confirmed to invert at 100m, which is exactly the file-size-against-RAM mechanism it named.

### E-09 — the meta-result: a 100m run does not rank a 1b run

- **Idea:** iterate on the 100m file, which is 10× cheaper, and promote the winners.
- **Measured:** **seven arms, seven disagreements.** Every arm above that separates at 1b either inverts at 100m (H3 parse, buffer size, table bits, `F_NOCACHE`) or vanishes into overlapping ranges (H1 cursor, H5 layout, H7 mmap, where mmap is 5.6× slower at 1b and *marginally faster* at 100m).
- **Verdict: KEEP as a binding rule for the remaining rounds.** Development iteration may use 100m; **no verdict may be taken from it**. The mechanism is not mysterious — at 1.4 GB the file is 5.8% of RAM and nothing is I/O bound, at 13.8 GB it is 53.5% and everything is — but the size of the effect is worth the rule. This is `spec.md:37` ("only end-to-end wall clock ranks solutions") biting one level deeper than it was written for: end-to-end at one tenth scale is still a microbenchmark.

## Hypothesis queue for `go-autoresearch-harness` and the optimization rounds

Ordered by the size of the gap each could close. The gap to shut is **0.742 s**, of which at most **0.988 s** is compute (1.742 measured minus the 0.754 s read floor).

1. **Fuse the name hash into the separator scan** (benhoyt r10's shape, `05-go-techniques.md`): the SWAR scan currently skips 8 bytes at a time and the hash then re-loads the first word. Unmeasured. Prediction: small, because the hash already reads only one word.
2. **The batch tokenizer** (`04-asm-kernels.md`'s biggest win, −40.4%/−40.7% on the official key set against staged SWAR): a whole-buffer dual-needle kernel emitting a token stream, drained into the table. This is the single most load-bearing untested assumption in the study, and `go-v2-kernels` owns it. The measured 1.93 ns Plan 9 call cost (`05-go-techniques.md`) says it must be called once per buffer, never per row.
3. **The 25% of the cores that are idle.** 11.3 of 15 busy. Nothing here measures whether that is I/O wait, the merge, or shard skew. A profile (`sample`, or `xctrace --template 'CPU Counters'`) refills this queue better than a guess; `go-opt-round-2` is defined as profile-driven for this reason.
4. **The correctness tax, measured rather than assumed.** The dual-needle scan (four more integer ops per word, counted from the expression, not measured) and `validTemp` (four to six byte compares per row) exist so this binary rejects exactly what the reference rejects. Neither has been priced against a trusting variant. Prediction: 3-8% of compute; if it is more, the shape of the check should change, not the contract.
5. **`unsafe.Add` pointer walks** in the fold loop: measured ceiling 4-16% on the scan in isolation (`05-go-techniques.md`), with the resliced-versus-unsafe gap there still unexplained.
6. **More workers than cores** (`-workers 20`, `-workers 30`): if part of the idle 25% is I/O wait, oversubscription hides it. Untried, and the cheapest experiment left.
7. **Buffer sizes above 4 MiB** at 1b: 8 and 16 MiB untried at the scale where 4 beat 1.
8. **A 1b 10,000-station file** to settle E-03's parked half and `05-go-techniques.md`'s working-set hypothesis. ~20 s of generation.
9. **Go's runtime map for the 10k case**: measured 3.7% FASTER than the best open-addressing arm on 10k names single-threaded (`05-go-techniques.md`). Never tested end-to-end, and would need item 8 first.
10. **The merge and the output.** 10,000 stations means 10,000 `map` inserts per shard at drain time and a sort; at 413 stations it is invisible, at 10k it may not be. Unmeasured.

### Added by `06-cross-disciplinary-transfer.md`

Four hypotheses, each borrowed from a field that solves the same shape of problem. They are numbered H-11 to H-14 so that the hypothesis id and the queue position are the same number; H1-H7 belong to `03-technique-recon.md` and H8-H10 are deliberately never issued, so a reader who meets an H-11 does not go looking for an H8 that was never written. The report also killed three candidates and parked two; those are not queue items and live in `06` and `PARKED.md`.

11. **H-11 — a static split weighted by core class.** This machine has two: 5 "Super" cores with 16 MiB L2 and 10 "Performance" cores with 8 MiB (`06-cross-disciplinary-transfer-data.txt` §1), and `reader.go` hands all 15 an equal share. Borrowed from morsel-driven parallelism, minus the dynamic dispatch that E-02 already killed here. **Prediction, derived:** if the fast class runs at `r` times the slow one, weighting is worth `(r/3 + 2/3)x` the equal-split wall clock — 3.3% at `r = 1.1`, 10.0% at `r = 1.3`, 16.7% at `r = 1.5`. `r` is unmeasured; measure it on a resident buffer first, then run the weighted split at 1b.
12. **H-12 — vectorize the table probe, not only the scan.** Compute a batch of hashes, issue all their bucket loads, then resolve, turning one dependent-load chain into N overlappable misses. Borrowed from vectorized query execution. **Prediction:** under 3% at 413 stations (0.3% load factor, the hot set stays in L2), over 10% at 10,000. The interesting half needs item 8.
13. **H-13 — a quotiented 32-byte entry.** Replace the 24-byte `[]byte` key header with an inline 8-byte prefix and a side array for the full name: 48 bytes becomes 32, the probed array shrinks 1.5x, and the hot compare is one word instead of a pointer chase. Borrowed from k-mer count tables. **Prediction:** 0 to 4% at 413. Deliberately close to E-03, which lost 4.6% doing something adjacent; the difference is that this adds no second array in the HIT path, and if it loses too then the neighbourhood is exhausted rather than untried.
14. **H-14 — double-buffer each worker.** `foldRange` calls `ReadAt`, waits, then folds, in one goroutine, alternating; a reader goroutine filling buffer B while the fold runs on buffer A makes the overlap explicit instead of leaving the OS to cover one worker's read with another's compute. Borrowed from DPDK's fill-while-you-process. **Prediction:** 5% up to a ceiling of 25% — 19.657 s of CPU over 15 busy cores is 1.310 s against the measured 1.742 s, which assumes every idle core is idle because of a read stall and is therefore a ceiling, not a forecast. Largest single-mechanism candidate found by the transfer pass.
