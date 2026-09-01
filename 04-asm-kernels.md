# 04 — Kernels: what the tokenizer costs per row on this chip, and which shape wins

Every number, the exact commands, the machine state and the full ten-run output: [`04-asm-kernels-data.txt`](04-asm-kernels-data.txt). All of it is **PROVISIONAL**: the session ran on battery, which `spec.md:42` makes provisional by rule. Per the loop's own lesson the ORDERING is the trustworthy part, and every ordering here is justified by separation rather than by the size of a median gap: in the run it is quoted from, the winner's slowest of ten runs is still faster than the loser's fastest of ten. That check is the one that matters, because within-benchmark full-range spread reaches 15.6% on the noisiest loop, so a median difference alone would not have established an ordering. One verdict is weaker than the rest and is labelled where it appears: the batch kernel's LOSS on the 10k stress case reproduced in direction across two runs but its ranges overlap in the noisier one.

The kernels live in `1brc/code/asm`, a Go module whose `make test` is the gate `spec.md:29` names for it. They are Go and Go's Plan 9 arm64 assembly rather than a separate C/clang harness, because the deliverable of this study is a Go binary: a kernel that wins in isolation but cannot be called cheaply from Go has not won anything, and `go-v2-kernels` is explicitly the ticket that has to measure "Plan9 asm or SWAR-in-Go where the call overhead kills the asm win". Measuring in Go from the start means that cost is in every number below instead of being discovered later.

This ticket owned H2, H3 and H6 from `03-technique-recon.md`. All three now have answers, and **two of the three came out against the recon's leaning**.

## The headline: the question was never SWAR versus NEON

It is **per-row versus batched**, and the whole effect is one instruction.

arm64 has no `PMOVMSKB`. Getting a compare result out of the vector unit costs a narrowing (`SHRN #4` over the 8×16-bit view, which packs each byte lane's `0x00`/`0xFF` into a nibble) and then a `VMOV` into a general register — and that `VMOV` sits in the scan loop's critical path, because the loop branches on it. There is no branch-on-vector instruction to avoid it with; Go's own `bytes.IndexByte` pays the same transfer once per 32-byte window.

That transfer was measured directly rather than argued about. `neonTransferProbe` is one 16-byte load, one compare, one narrow and one `VMOV`, and nothing else; `BenchmarkNEONTransferFloor` adds it to an otherwise unchanged staged-SWAR loop. **One vector-to-general round trip costs 1.080 ns/row on the 413-station corpus** (0.825 on the 10k stress case). That is the floor under any per-row NEON kernel, before it does one byte of useful work.

Set the three measured results next to it and they tell one story:

| what changed | 413 stations | 10k stress case |
|---|---|---|
| 16-byte NEON scan instead of 8-byte SWAR, per row (H2) | **+1.830 ns/row (+16.3%)** | **+3.185 ns/row (+23.1%)** |
| one bare vector-to-general transfer per row | +1.080 ns/row (+8.2%) | +0.825 ns/row (+4.9%) |
| 32-byte dual-needle compare, amortised over every row in the window | **−4.24 to −4.37 ns/row (−40.4% / −40.7%)** | +1.74 to +1.83 ns/row (+12.1% / +13.8%) |

Per row, the wider window does not pay for the transfer it forces. Batched, the same vector compare — now answering **both** `;` and `\n` for roughly 2.3 rows out of one pair of transfers — is the largest single win measured anywhere in this study so far: **6.25-6.37 ns/row against staged SWAR's 10.49-10.74** on the official key set, across the two post-fix runs.

## H2 — falsified: 8-byte SWAR beats a 16-byte NEON compare, and by more where it should have lost

**Prediction (`03-technique-recon.md`):** NEON should win because SWAR needs a second 8-byte load on 34.1% of official names where NEON needs a second 16-byte window on only 1.0%; the recon deliberately hedged, naming the per-row vector-to-general transfer as the reason it might go the other way.

**Measured:** SWAR wins by 16.3% on the 413 set and by **23.1%** on the 10k long-name set. The direction is wrong and so is the trend — the margin *grows* on the corpus where names average 51.1 bytes and NEON's wider window should be at its most valuable.

`TokenizeStagedSWAR` and `TokenizeStagedNEON` differ in exactly one line, so the delta is the scan and nothing else. The mechanism the recon named is confirmed as the cause, and it is bigger than the window advantage in both directions: the transfer plus the non-inlinable assembly call is 1.080 ns/row measured bare, against a 1.830 ns/row loss on 413. On the 10k set the two loops each run several windows per row, so the wider window really is doing less work per byte, and NEON still loses by more — which says the transfer is not a fixed startup cost being amortised away but a per-window cost in the loop's dependency chain.

The recon's hedge was right for the right reason, and the hedge should have been the prediction.

**Not killed, parked as [`PARKED.md`](PARKED.md) P-01 with a revive trigger.** The rescue is not a better NEON scan, it is fewer transfers per row, and that rescue is already measured: it is the batch kernel below, which wins by about 40.5%.

## H3 — split, and the split is the finding

**Prediction:** merykitty's branchless parse costs ≤2 ns/row, wins outright, and no NEON variant beats it.

**Measured, and it depends entirely on the input:** the branchless parse is **15.2% slower** than a plain scalar digit loop on the 413 corpus, and **16.4% faster** on the 10k corpus. Both loops hold the separator scan fixed at SWAR, so the spread is the parse.

The parse's arithmetic is identical in both runs; only the data changes. What changes with it is how predictable the field's shape is — sign present or absent, one whole digit or two. The scalar loop is branchy and wins when the branch predictor is right; the branchless version pays the same eight-ish operations no matter what and wins when it is not. So merykitty's parse is not faster arithmetic, it is **insurance against misprediction**, and its value is set by the temperature distribution rather than by the parse.

That matters for the real file, and this ticket cannot settle it: at 1B rows the loop is running 15 shards with a hash table competing for the same branch predictor and the same cache, which is not the condition either microbenchmark reproduces. Per `spec.md:37` neither variant is discarded. The scalar parse is not dead and the branchless parse is not proven; both go to `go-v1-parallel` behind one flag.

The **≤2 ns/row** half of H3 is **unanswered**, and honestly so: only within-benchmark deltas are trustworthy at this scale (see Method below), and no benchmark here isolates the parse's absolute cost from the loop it sits in. Answering it needs a parse-only loop over pre-located fields, which was not built.

**The NEON parse variant was not built, and that is a deviation from the recon's stated test for H3.** It is parked on a measured argument rather than skipped: any per-row NEON kernel pays 1.080 ns/row before doing useful work, and the *entire* measured advantage of one parse over the other is 1.7-3.3 ns/row. A NEON parse would have to beat the branchless parse by more than the whole spread between the two existing parses just to break even on its transfer. That is a number, not a hunch, and it is recorded as [`PARKED.md`](PARKED.md) P-02 with the revive trigger: if the batch shape wins in `go-v1-parallel`, a NEON parse inside a batched loop amortises the same transfer and the argument no longer applies. P-02 also records that this is a BOUND and not a measurement of the kernel itself.

## H6 — jerrinot was right about the direction, and the crossover he suspected does not happen at two lines

**The inherited question** (`jerrinot.java:349-352`, quoted in the recon): masking a name word by shifting versus indexing a 9-entry lookup table, with the author's own note that bit-twiddling should win one line at a time and lose when several are in flight because load latency gets hidden.

**Measured:** shift wins both, but the margin collapses.

| | shift | lut | shift's margin |
|---|---|---|---|
| one line in flight | 2.267 ns/row | 2.780 ns/row | **22.6%** |
| two interleaved | 1.413 ns/row | 1.579 ns/row | **11.7%** |

Interleaving two independent chains hides roughly half of the table's disadvantage, which is the effect jerrinot described, but on this chip it does not go all the way to a crossover. His hypothesis is confirmed in direction and refuted in its second half at a depth of two.

Getting this measurement right took two attempts and the first one pointed the other way, which is worth recording because it is the repo's own recurring failure. Driving the two variants through a `func` value made the benchmark a benchmark of the indirect call: both variants landed at 2.2 ns/row a hundredth of a nanosecond apart, and a rerun flipped the winner. A one-instruction kernel cannot be measured through a call that costs more than it does. The benchmarks now call every kernel directly so it inlines, and the dependency chain runs `acc → word → separator position → mask → acc`, which is the real loop's shape and the only one in which a table's address is not known early enough to prefetch.

## The batch tokenizer: gigatoken's shape is the one that pays

`03-technique-recon.md` found nothing portable in gigatoken itself and recorded it as a scale reference. What it does contribute is a **shape**: scan a window, emit a token stream, aggregate from the stream, rather than handling one row end to end at a time. Built here as `TokenizeBatch` — one 32-byte load compared against `;` and `\n` in the same pass, both syndromes drained in address order — it is the largest win in this report and also the most input-sensitive.

- **413 stations: 6.253 against 10.490 (−40.4%) in one post-fix run and 6.367 against 10.735 (−40.7%) in the other.** One pair of transfers serves ~2.3 rows. Both runs are disjoint over ten runs each.
- **10k stress case: 15.110 against 13.280 (+13.8%) and 16.035 against 14.300 (+12.1%).** Names average 51.1 bytes, so a 32-byte window no longer spans a row; the amortisation inverts into overhead and the token-stream write is left with nothing to pay for it. The direction reproduced in both runs; the ten-run ranges overlap in the noisier one, so this crossover is a reproduced direction rather than a separated margin.

The crossover is structural, not mysterious: the batch shape wins exactly while a window holds more than one row. That makes it a property of the key set, and the official key set is comfortably on the winning side.

Two things were fixed before this number was allowed to stand, both of which would have made it a lie:

1. **The token could not locate the name.** `Token` originally carried only the name length and the value, which is fine for a staged tokenizer whose driver already holds the row's position — and useless for a batch kernel, whose drain loop does not. The batch kernel was being measured against a job it was not doing. `Token` now carries `Start`, the batch kernel fills it, and the benchmark's drain loop reads it. Three `int32`s keep the token at 12 bytes.
2. **The benchmark's own row count was unverified.** `ns/row` is reported as elapsed divided by the corpus row count, so a driver that stopped early would have published a per-row cost for work it never did. `TestBatchBenchLoopCoversEveryRow` reproduces the benchmark's driver loop exactly and checks both the row count and every token against the scalar reference.

## Method: what is and is not evidence here

- **Every delta quoted is same-run and same-shape:** two loops from one invocation of the suite, with one thing varied. Both halves matter, and both were checked rather than assumed.
- **Every verdict-bearing pair is disjoint over all ten runs, with one labelled exception.** The winner's slowest run beats the loser's fastest one in every comparison in the main run, the narrowest separation being H6 interleaved at 0.140 ns/row. This is the check that carries the orderings, because within-benchmark full-range spread runs from 0.8% to 15.6%. The exception is the batch kernel's 10k loss after the repair below: disjoint in one post-fix run, overlapping in the other, so it is reported as a reproduced direction rather than a separated margin.
- **The batch kernel was repaired mid-report and every batch number here is post-repair.** It ended a name at the LAST `;` in a row where every other kernel ends it at the first, which is `gen.Aggregate`'s rule; neither key set produces a name containing `;`, so the corpus agreement tests passed either way. The repair (commit `730d166`) adds one predictable branch and costs about 1.8 points of the headline: −42.3% measured before it, −40.4% and −40.7% in the two runs after. Only `TokenizeBatch` changed, so every non-batch number keeps the original run, and the superseded figures are in `CORRECTIONS.md` rather than deleted.
- **The harness's own floor was measured with a control.** Two byte-identical copies of the mask loop, in different closures, agree to **0.001-0.002 ns/row**. So closure placement is not moving these numbers, and H6's margins (0.513 and 0.165 ns/row) clear that floor by 83x to 513x. It was run because H6's interleaved margin was small enough to deserve the doubt that it was code layout rather than the kernel.
- **Run-to-run drift is a different and much larger thing:** the same mask benchmark measured 2.267 ns/row in the main run and 2.102 in the control run, 7.9% apart, from identical code, on a battery-powered machine whose load average moved from 3.4 to 5.0 during the main run and from 2.3 to 2.5 during the control. Numbers from different invocations are never compared here.
- **Correctness gates the ranking, per `spec.md:36`.** Every kernel is checked against a scalar reference over both corpora, over adversarial input the corpus cannot produce (high-bit UTF-8, which real station names have; names at every length around both window boundaries; `:` and `<` neighbours of `;`), and over 200,000 random inputs. All 1999 legal temperatures are checked value-and-offset at every alignment.
- **The one bug class Go cannot catch was tested for directly.** A kernel reading past its slice is invisible when the slice is a window into a larger buffer, which is what every fuzz test hands it. `TestNEONIndexSemicolonStopsAtTheSliceEnd` maps two pages, `PROT_NONE`s the second and puts the input flush against the boundary, so an over-read segfaults. It is what catches the 16-byte guard being weakened.
- **Every kernel was mutation-tested**: 36 mutants across the Go and the assembly, 29 caught. The seven survivors were each shown to be equivalent implementations rather than gaps (a SWAR stride of 7 overlaps windows and stays correct; `CMEQ` is commutative; `BLO` for `BLS` byte-scans the last window instead of vectorising it; an equality branch that no input can reach), and two of them confirm the syndrome folding: `0x40100402` instead of `0x40100401` moves each lane's bit within the pair that `ctz>>1` discards, and `VADDP` with the same register as both operands writes the same 64 bits into `D[0]` and `D[1]`, so reading either lane is the same read.
- **The 10k corpus is a long-name stress case, not a second realistic input.** `gen.Synthetic10k` produces names averaging 51.1 bytes with 68.9% over 32, against the official set's 8.0-byte mean. `02-baseline-data.txt:115-118` already recorded that it is not a byte-compatible reproduction of upstream's generator; the length distribution is the axis this report adds. It earns its place by being the adversarial end of the name-length axis, and no verdict here rests on it alone.

## What this hands forward

`go-v1-parallel` and `go-v2-kernels` inherit a different starting point than the recon expected.

| | what to build | why |
|---|---|---|
| separator scan | 8-byte SWAR, not a per-row NEON scan | H2 falsified, 16.3-23.1% |
| whole-row shape | batch a 32-byte dual-needle window into a token stream | −40.4% / −40.7% on the official key set, the largest win measured |
| temperature parse | both, behind one flag | H3 split; the winner is set by branch predictability, which only the real 15-shard loop settles |
| name mask | shift, not a lookup table | H6, 22.6% single / 11.7% interleaved |

## Threads left open

- The batch kernel's win is measured with a 4096-token buffer drained immediately. A real aggregator folds each token into a hash table instead, so the drain loop competes with the table for L1 and for the branch predictor. Whether −40.5% survives that is `go-v1-parallel`'s to measure, and it is the single most load-bearing untested assumption in this report.
- The batch kernel processes one 32-byte window at a time. Two or four windows per transfer pair would amortise further and the crossover with long names would move; untried.
- H3's absolute cost (`≤2 ns/row`) is unanswered. It needs a parse-only loop over pre-located fields, which is a different harness from the one built here.
- No NEON temperature parse exists to falsify the parked argument against it. The argument is a measured bound, not a measurement of the thing itself.
- One within-run gap is measured and unexplained: `BenchmarkTokenize/staged-swar` and `BenchmarkParseTemp/branchless` compute the same tokenization and differ by 1.930 ns/row, with the FASTER one being the loop that does strictly more work (a non-inlinable call plus two more accumulator adds). The control above rules out closure placement, so it is something real about the two loop bodies. No verdict here depends on it, because every verdict is a same-shape pair, but it is the reason nothing in this report is derived by subtracting two differently-shaped loops.
- The staged-NEON tokenizer pays a non-inlinable assembly call that staged-SWAR does not, and the transfer floor probe pays one too, so "transfer" throughout this report means transfer-plus-call. Separating them needs the same kernel written twice, once in Go asm and once as a compiler intrinsic, and Go has no intrinsic for it.
