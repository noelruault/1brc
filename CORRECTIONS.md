# Corrections and falsifications — 1brc

Per `spec.md:41`: a published number or claim later found wrong is corrected at EVERY site that publishes it, and gets one entry here. Empty is a valid state; it means nothing published was later disproved. A non-empty file is not an embarrassment, it is the part of the record that proves the rest was checked.

One entry per falsified claim: what was claimed, where it was published, what disproved it, and the commit that corrected it.

---

## C1 — "88.6% of station names are longer than 8 bytes"

- **Claimed:** that 88.6% of the 413 official station names exceed 8 bytes, offered as the rationale for hypothesis H2 (a 16-byte NEON compare should beat an 8-byte SWAR word on the name scan).
- **Published in:** a draft of `03-technique-recon.md`, in the H2 paragraph. Caught before the commit landed, so it never reached a pushed commit.
- **What disproved it:** direct computation over the key table in `code/gen/stations.go`, recorded as probe 5 in `03-technique-recon-data.txt`. Measured: **34.1%** longer than 8 bytes (141 of 413), mean 8.0 bytes, max 26 (`Las Palmas de Gran Canaria`), and only 1.0% longer than 16 bytes. The claim was wrong by a factor of 2.6.
- **Why it happened:** the figure was invented to support a hypothesis that felt right, in a file whose whole purpose is to hold checkable claims. Nothing measured it, and nothing had to: the key set is 413 rows in a file already in the repo.
- **Consequence:** H2's prediction changed direction. It had been "NEON wins on names"; it now says the margin may go either way, because SWAR's second load is an L1 hit while NEON pays a vector-to-general-register transfer on every row. The correction also surfaced a fact the wrong number hid: 1.0% of names exceed 16 bytes, so thomaswue's 16-byte hash window has a slow path that fires on ~1% of rows rather than never.
- **Corrected in:** `61dcbb6` (report and data companion, same commit as the claim).

## C2 — "under 1.0 s wall clock, warm page cache" as the stated target

- **Claimed:** that our target is 1B rows in under 1.0 s with a **warm page cache**, mirroring the official eval's method.
- **Published in:** `01-definition.md:91` (the "our target" paragraph), and the corresponding rule in `spec.md:35` as originally written.
- **What disproved it:** `02-baseline.md` / `02-baseline-data.txt`. The 1b file is 13,795,610,267 bytes = 12.85 GiB, which is 53.5% of this machine's 24.00 GiB. After two full sequential passes, free memory is 57.8 MiB and only 9.47 GiB sits on the active+inactive lists: macOS evicts the head of the file while the tail is being read, so the warm-cache state is not reachable here at all. Worse for the premise, the page-cached path is **slower** than an uncached one — 1.126 s ± 0.007 s page-cached against 754.4 ms ± 8.8 ms with `F_NOCACHE` and 15 parallel readers, a 1.49x inversion of the usual assumption.
- **Consequence:** the target is a wall clock under a **named, reproducible storage state**, not under a state this machine cannot enter. `spec.md:35` was amended accordingly by the operator, and every published number now names its storage state and reader configuration.
- **Corrected in:** this commit (`01-definition.md` at its site); the rule itself in the operator's `9c9aded`.

## C3 — "the batch tokenizer is worth −42.3% on the official key set"

- **Claimed:** that gigatoken's batch shape tokenizes the 413-station corpus at **6.475 ns/row against staged SWAR's 11.225, −42.3%**, published as the largest win measured anywhere in this study and as the shape `go-v1-parallel` should start from.
- **Published in:** `04-asm-kernels.md` (the headline table, the H2 rescue sentence, the batch section, the hands-forward table and the threads list), `04-asm-kernels-data.txt` (medians and deltas), and `docs/1brc/built.md` — all in commit `b4be167`, which was pushed.
- **What disproved it:** the review gate on the same group. The number is a correct measurement of a kernel that was **wrong**: `TokenizeBatch` overwrote its pending separator on every `;` it drained, so it ended a name at the LAST separator in a row while `TokenizeScalar`, `TokenizeStagedSWAR` and `TokenizeStagedNEON` all end it at the first, which is `gen.Aggregate`'s rule. On `Ab;cd;1.0` the batch kernel reported a 5-byte name and the others a 2-byte one, so it would have attributed the row to a different station. Neither key set produces a name containing `;`, so `TestTokenizersMatchTheScalarReference` and `TestBatchBenchLoopCoversEveryRow` both passed over both corpora with the bug present — the benchmark was pricing work the kernel was not doing correctly.
- **Measured after the repair:** **−40.4%** (6.253 against 10.490) and **−40.7%** (6.367 against 10.735) in two post-fix runs, both disjoint over ten runs each. The verdict survives; the magnitude drops by about 1.8 points, which is the one predictable branch the repair adds to the drain loop. The 10k crossover also survives in direction (+13.8% and +12.1%) but its ranges overlap in the noisier of the two runs, so it is now reported as a reproduced direction rather than a separated margin.
- **Why it happened:** the kernel was checked only against inputs the generator can produce. The repo already knew this failure mode — `docs/1brc/lessons.md` records the identical first-vs-last-`;` divergence being caught in `go-skeleton` one group earlier, by mutation testing rather than by the corpus — and the batch kernel was written afterwards without the guard.
- **Corrected in:** `730d166` (the kernel, plus `TestEveryKernelEndsTheNameAtTheFIRSTSemicolon`, which fails when the guard is reverted while the corpus agreement tests still pass), and this commit at every site above.

## C4 — "a `1<<17` entry array is 4 MiB, and splitting it out shrinks the probed array 4x"

- **Claimed:** that the per-shard table's `entry` is 32 bytes, so a `1<<17` entry array is 4 MiB and a `1<<14` one is 512 KiB, and that H5's separate 8-byte hash array therefore touches "a quarter" as much memory.
- **Published in:** `07-experiment-ledger.md` E-03's idea and prediction lines and E-07's prediction line, `05-go-techniques.md` (the working-set hypothesis and the hands-forward table), `1brc/code/go/table.go`'s `table` doc comment, and `docs/1brc/handoff.md`'s `go-recon` deviations line.
- **What disproved it:** `unsafe.Sizeof(entry{})` on this machine, recorded with its throwaway test in `06-cross-disciplinary-transfer-data.txt` §2. `entry` is **48 bytes**, not 32: `key []byte` is a 24-byte slice header on arm64, plus `min`/`max` int32, `sum` int64 and `count` int32 is 44, padded to 48 by `sum`'s alignment. So a `1<<17` array is **6.00 MiB**, a `1<<14` array is **768 KiB**, and the split-array ratio is **6x**, not 4x.
- **Consequence for the verdicts: none, and that is worth stating.** E-03's split layout still lost 4.6% at 413 stations and E-07's big table still won 7.2%; both were measured, and only the arithmetic *around* them was wrong. The correction makes E-03's mechanism argument stronger, not weaker, which is exactly why it needed catching: a number that supports your conclusion is the one nobody re-checks.
- **Why it happened:** the size was computed by counting the fields' logical widths and never checked against the compiler. `24 + 4 + 4 + 8 + 4` was read as "about 32" because four fields are 4 or 8 bytes and the slice header was counted as a pointer.
- **Corrected in:** this commit, at all six sites, each marked inline. `docs/1brc/handoff.md`'s copy is corrected in this cycle's own handoff block rather than by editing the historical block, which the handoff protocol writes before its review.

## C5 — "a 1-billion-row 10,000-station file is ~20 s of generation"

- **Claimed:** that queue item 8, the 1b 10k-station file that E-03's parked half and hypotheses H-12, H-13, P-03 and queue items 9 and 10 all depend on, costs about 20 seconds to generate, and is otherwise the same experiment as the 413-station one with a bigger key set.
- **Published in:** `07-experiment-ledger.md` E-03's revive trigger and queue item 8, and `docs/1brc/handoff.md`'s `x-transfer` threads line — all in commits `f786d4e` and `5528b58`, both pushed.
- **What disproved it:** arithmetic over a file this study already generated and recorded. `02-baseline-data.txt` records `measurements-10k-stations-10m.txt` at **570,941,611 bytes for 10,000,000 rows**, so the synthetic 10k names cost **57.09 bytes per row** against the official set's 13.80. A billion of those rows is **57.09 GB**, not 13.8 GB. At the 725 MB/s the generator was measured at, that is **~79 s**, four times the published figure.
- **The larger error is not the 79 s.** At 57.09 GB the file is **2.22× this machine's 24 GiB of RAM**, where `measurements-1b.txt` is 53.5% of it, and its read floor alone is **3.12 s** at the 18.3 GB/s measured in `02-baseline.md` — three times over the study's whole target before any compute runs. So the file could not have answered the question it was queued for: no arm measured on it is comparable with the 1.742 s headline, and E-09's own mechanism (file size against RAM) says the ranking need not even carry. Holding **bytes** constant instead — 241.6M rows at 13.80 GB — reproduces the regime the 1b file established, and is what the dependent items actually want.
- **Consequence for the verdicts: none.** Nothing was measured on this file; every claim resting on it was already labelled parked, blocked or predicted. What changes is the queue: item 8 stops being the cheap unblocker three other items assumed, and it now carries a design choice (equal rows or equal bytes) that the round must make explicitly.
- **Why it happened:** the cost was estimated from the 13.8 GB the study measures everything on, without checking that the 10k key set's rows are 4.1× wider — and the recorded byte count that shows it had been sitting in `02-baseline-data.txt` since `env-data`. A generation estimate reads like bookkeeping, so nobody re-derived it.
- **Corrected in:** this commit, at both `07-experiment-ledger.md` sites, marked inline, with the derivation in the queue board. `docs/1brc/handoff.md`'s copy is corrected in this cycle's own handoff block rather than by editing the historical block, following C4.
