# 05 — How far raw Go goes: the table is the lever, `unsafe` is not

Every number, the exact commands, the fetch hashes and the full ten-run output: [`05-go-techniques-data.txt`](05-go-techniques-data.txt). All of it is **PROVISIONAL** (battery, `spec.md:42`). Six probes ran in a throwaway module with a `replace` onto `1brc/code/gen`, so every one of them measured bytes the authoritative generator produced; their sources are transcribed into the data companion because `/tmp` is not durable and a number nobody can re-derive is worthless here.

Two results are the reason this report exists, and both go against what the corpus does.

Every percentage below is relative to the arm being REPLACED, so a negative number is what the change saves and a positive one is what it costs. `07-experiment-ledger.md` uses the same convention with the default configuration as the baseline.

## The corpus, and how it was chosen rather than recalled

The Go entries were picked by a recorded query — `api.github.com/search/repositories?q=1brc+language:Go&sort=stars` — not from memory, and the result is in the data companion. Three sources were read for TECHNIQUE only, per `spec.md:39`; none of their code is in this repo.

| source | what it is | their headline, on THEIR machine |
|---|---|---|
| `benhoyt/go-1brc` r1→r10 | ten progressive Go revisions, no `unsafe`, no mmap | r1 1m39.5s, r9 **2.894 s**, r10 **2.497 s** on 13.16 GB |
| `shraddhaag/1brc` (+ its write-up) | channels, chunking, custom parser, Go maps | ~14 s, from >6 min |
| `automataIA/1brc-rs` | Rust, mmap variants vs a safe positional-read variant | mmap+flat table **1.87 s**; safe pread streaming **2.105 s ± 0.034** (Ryzen 5 7600, WSL2) |

Those are facts about their hardware and are never compared against our wall clock (`spec.md:34`). What transfers is the *shape*, and three shapes matter:

- **benhoyt r9/r10** put the whole hot loop in one function per shard: split the file into one contiguous range per goroutine, read it in 1 MiB chunks with a carried-over remainder (`bytes.LastIndexByte(chunk, '\n')`, then `copy(buf, remaining)`), aggregate into a **1<<17 linearly-probed open-addressing table** private to the goroutine, and merge into a `map` only at the end. r10 adds SWAR semicolon-finding and hashes **only the first 8 bytes** of the name, resolving collisions with a full `bytes.Equal`. That is the same prefix-hash design H4 asks about, shipped by someone else, and it is the single most transplantable thing in the corpus.
- **shraddhaag's** journey is the negative result: her final bottleneck was Go map access, and her own "further ideas" list is *encode station names as numeric keys, replace the map with a faster one*. Two independent Go implementations arriving at the map as the wall is worth more than either arriving there alone.
- **automataIA's safe variant** is our I/O strategy, independently: `read_exact_at` (pread) over disjoint ranges, **one reusable 4 MiB buffer per worker**, one local map per worker, merge and sort at the end, nothing shared in the row loop. On Linux their mmap variant still wins, but by **11%**, not by the 5-9× margin mmap LOST by here on a scan (`02-baseline.md`). Their 4 MiB buffer is also a datapoint against our sweep, which only tried 1 MiB and 8 MiB.

## The hash table costs more than the parse, and Go's own map is not the worst option

Four table shapes behind one identical scan, so the delta between any two is the table and nothing else. Ten runs each, one invocation, `ns/row`, mean with the full range:

| table shape | 413 stations | 10k stress case |
|---|---|---|
| `map[string]*acc` (the skeleton's shape) | 20.76 [20.62-20.99] | **27.08 [26.98-27.30]** |
| open addressing, FNV-1a over the **whole** name | 19.44 [19.38-19.67] | 68.52 [68.37-68.72] |
| open addressing, **8-byte prefix** hash, 1<<14 buckets | **17.48 [17.40-17.71]** | 30.67 [30.49-31.11] |
| the same, 1<<17 buckets | 17.53 [17.47-17.76] | 28.07 [27.93-28.33] |
| scan and parse with no table at all (floor, not a verdict) | 13.28 [13.19-13.62] | 16.08 [15.93-16.43] |

**Measured, 413 stations:** the prefix-hashed table beats Go's map by **15.8%** and beats full-name FNV by **10.1%**. Every pair is disjoint over its ten runs — the winner's slowest beats the loser's fastest — except the two bucket counts, which overlap and are therefore the same. Against the no-table floor, the table is **4.2 ns/row of the 17.5**, so it is the largest single component of the hot loop after the scan itself.

**Measured, 10k stress case, and this is the result that overturns the plan:** Go's map **wins** by 3.7% (disjoint) against the best open-addressing variant, and full-name FNV is **2.44× slower** than everything else. Two separate mechanisms, one measured and one hypothesised:

1. FNV walks every byte of the key, and these names average 51.1 bytes, so it pays ~51 multiplies per row where the prefix hash pays one. That is arithmetic, not cache behaviour, and it kills full-key hashing for any key set with long names. **Measured.**
2. Between the two prefix-hashed variants, going from 1<<14 to 1<<17 buckets is worth **8.5%** on the 10k set (disjoint) and **nothing** on the 413 set (overlapping ranges). 10,000 entries in 16,384 buckets is a 61% load factor, where linear probing degrades; in 131,072 buckets it is 7.6%, where it does not. That is the confound this report nearly published as "open addressing loses on long names" — it is worth 8.5% of the 13% gap. **Measured.**
3. Even with the load factor fixed, Go's map still wins on 10k. The **hypothesis** is working-set size: 1<<17 entries × 32 B is a 4 MiB array touched at random, larger than any cache level that matters, while a Go map holding 10,000 live entries is compact. That predicts something testable and it is exactly H5's split-array layout: a separate hash array of 8-byte slots is 1 MiB instead of 4 MiB, so the probed array shrinks ~4× and the entry array is touched once per row instead of once per probe. **Not measured here** — H5 now carries a mechanism and a prediction rather than an intuition.

The design consequence is not "use a Go map". It is that **the official 413-station key set and the 10k stressor want different tables**, and the official set is what the target is defined against. Build the prefix-hashed table, size it for a low load factor, and keep the Go map behind a flag as the 10k-station fallback rather than deleting it.

## H4 — answered, and it is free: 16 bytes are enough, 8 nearly are

A static property of the two key sets, computed rather than benchmarked (`03-technique-recon.md` called this the cheapest experiment in the study and it was):

| key set | names | distinct 8-byte prefixes | distinct 16-byte prefixes | names > 8 B | names > 16 B | worst prefix group |
|---|---|---|---|---|---|---|
| 413 official | 413 | 412 | **413** | 141 (34.1%) | 4 (1.0%) | 2, at `"Alexandr"` (8 B) |
| 10k synthetic | 10000 | **10000** | 10000 | 9319 (93.2%) | 8535 (85.3%) | 1 |

**H4 is confirmed and then some.** Sixteen bytes separate both key sets completely, so a 16-byte hash needs no collision handling at all beyond the table's own probing. Even **eight** bytes leave exactly one collision on the official set — `Alexandra` and `Alexandria` — which a full `bytes.Equal` resolves in one extra probe, one station out of 413. This is why r10's 8-byte hash works, and it means the hot loop does not need a second load for the 34.1% of names longer than 8 bytes *for hashing purposes*; it still needs to find the separator and to compare the full key.

One honest limit: the 10k set's perfect 8-byte separation is an artifact of `gen.Synthetic10k` synthesising names randomly, not a fact about real 10,000-station data. The 413 number is the one drawn from upstream's key set and the one to trust.

## `unsafe` buys 4-16%, and the bounds check is only part of the reason

Three spellings of the same SWAR separator scan, same arithmetic, one invocation:

| scan spelling | bounds checks left | 413 stations | 10k stress case |
|---|---|---|---|
| `binary.LittleEndian.Uint64(b[i:])`, indexed | **3** (two on the word load, one on the tail byte) | 11.67 [11.63-11.72] | 15.98 [15.87-16.02] |
| guard on `len`, load from the front, `rest = rest[8:]` | **0** | 11.06 [11.00-11.18] | 16.00 [15.86-16.15] |
| `unsafe.Add` pointer walk | **0** | **10.64 [10.60-10.79]** | **13.41 [13.34-13.58]** |

**Measured:** on the 413 set all three are disjoint, and reslicing — plain Go, no `unsafe` — recovers **5.2%** of the indexed version's loss while `unsafe` recovers **8.8%**. On the 10k set reslicing recovers *nothing* (its range overlaps the indexed version's) and `unsafe` wins by **16.1%**.

The bounds check is real and `-gcflags=-d=ssa/check_bce` names it precisely: the indexed loop keeps three checks, the resliced loop and the unsafe loop keep none. But **the mechanism is only half established**, and the report says so rather than rounding it off: a loop with zero bounds checks is still 3.8% slower than the pointer walk on 413 and 16% slower on 10k, so something other than the check separates them. The plausible candidate is per-window slice bookkeeping (pointer *and* length updated every 8 bytes, against one index increment), but nothing here measures it and it is not claimed.

For the design: `unsafe` is worth about **1 ns/row** on short names, which is not nothing against a 1.0 ns/row headline budget — but it is a fifth of what the table shape is worth, and it is available in plain Go at more than half strength. v1 is built without `unsafe`; the pointer walk is a `go-opt-round` experiment with a measured 4-16% ceiling, not a v1 dependency.

## GC is a non-event in this design, so `GOGC=off` is not a lever

**Measured:** aggregating 524,288 rows into a preallocated table, keys copied only on first sight, ran **zero collections** (`NumGC` 5 → 5) with **414 allocations** total: the table, plus one key copy per distinct station. 790 KB of `TotalAlloc` for half a million rows.

`GOGC=off` and `debug.SetGCPercent(-1)` appear all over the corpus, and this measurement says why they are not our lever: they are a fix for a design that allocates per row or per chunk. A design that allocates once per shard has nothing for the collector to do, and the correct treatment of the technique is to **keep the allocation profile flat and never need it**. It stays a cheap end-to-end check in v1 (one environment variable, one hyperfine run) precisely because a null result there confirms the shape is right.

## A Plan 9 assembly call costs 1.93 ns, which decides `go-v2-kernels` before it starts

**Measured**, same-run pair, one-instruction addition either side of the boundary: through assembly **2.145 ns/call** [2.144-2.156]; inlinable Go **0.2185 ns/call** [0.2180-0.2207]. The call is **1.93 ns**, disjoint by two orders of magnitude of the ranges.

Set that against the budget: **1.93 ns/row is roughly twice the entire 1.0 ns/row headline budget** and about 13% of the 15.0 ns/row per-core budget derived below. **Any assembly kernel called once per row is dead on arrival in Go**, regardless of how good the kernel is. Assembly has to be called once per *chunk* — a whole-buffer tokenizer that returns a token stream, which is precisely the batch shape `04-asm-kernels.md` measured as its biggest win, for an independent reason.

This number also needs reconciling with an existing one rather than sitting next to it: `04-asm-kernels.md` measured the vector-to-general transfer *plus* its call at **1.080 ns/row** by adding a probe call to a staged-SWAR loop. A bare call measured alone costs 1.93 ns. Both are real; they differ because the loop the probe was added to had independent work to overlap with, and this benchmark's loop is a pure dependency chain with nothing to hide behind. So **1.93 ns is the unhidden cost and ~1.08 ns is what survives when there is other work in flight** — a range, and the mechanism (instruction-level parallelism hiding part of the call) is a **hypothesis**, not a measurement. Neither figure is wrong; quoting either without its loop shape would be.

## The arithmetic that reframes the whole target

**Derived, not measured.** The headline budget is 1.0 s for 1e9 rows = 1.0 ns/row of *wall clock*. But the machine has 15 cores, so a perfectly sharded loop gives each core 66.7M rows and **15.0 ns/row of single-core work** to spend. That single division changes the reading of every number in this study:

- The skeleton's 26.1 ns/row (`go-skeleton`) is **1.74× over budget**, not 26× over.
- The best table shape measured here — 17.48 ns/row, *including* a `bytes.IndexByte` scan — is **17% over budget** on the official key set.
- Substituting the SWAR scan for `bytes.IndexByte` is worth about 1.6 ns/row in this loop family, which would land ≈15.9 ns/row, **6% over**.
- The I/O floor is 754 ms of the 1000 ms (`02-baseline.md`), so compute must **overlap** I/O almost completely; the 15 readers and the 15 aggregators are the same 15 goroutines, and any design that reads then processes has already lost.

Every bullet after the first is a **projection across benchmark shapes**, which this repo has been burned by exactly once already (`04-asm-kernels.md` Method: a modelled figure pointed the wrong way, +7.4% modelled against −9.2% measured). They are stated to set expectations for `go-v1-parallel`, and `go-v1-parallel`'s hyperfine run replaces them.

## What this hands forward to `go-v1-parallel`

| decision | what to build | why, and how strong |
|---|---|---|
| I/O | 15 goroutines, disjoint ranges, `pread` with `F_NOCACHE`, reusable per-worker buffer | measured winner in `02-baseline.md`; independently the shape `automataIA/1brc-rs`'s safe variant uses |
| buffer size | try 4 MiB alongside 1 MiB | their choice; our sweep only covered 1 and 8 MiB |
| table | per-shard open addressing, 8-byte prefix hash, full `bytes.Equal` compare, ≤10% load factor | measured −15.8% against a Go map on 413; H4 says 8 bytes leave one collision in 413 |
| 10k fallback | keep the Go map reachable by flag | measured: it WINS by 3.7% on 10k stations |
| table layout | H5's split hash/entry arrays, now with a predicted mechanism (4 MiB probed array → 1 MiB) | hypothesis; the 10k gap is the evidence it is worth trying |
| `unsafe` | not in v1 | measured ceiling 4-16% on the scan only; reslicing gets half of it in safe Go |
| GC | preallocate, then verify `NumGC` stays flat end-to-end | measured zero collections at probe scale |
| assembly | never per row; per chunk only | measured 1.93 ns/call against a 15.0 ns/row per-core budget |
| shape | read and aggregate in the SAME goroutine, no channels of rows | derived: the I/O floor is 75% of the budget, so I/O and compute must overlap |

H1 (shared-cursor segments vs a static split) and H7 (mmap end-to-end) are untouched here and stay `go-v1-parallel`'s, unchanged.

## Threads left open

- The resliced-versus-`unsafe` gap (3.8% on 413, 16% on 10k) is measured with zero bounds checks on both sides and **unexplained**. It is the only unattributed margin in this report.
- Nothing here measured a hash function against a *sharded* loop; every table number is single-goroutine. Fifteen tables in fifteen cores contend for L2 and the memory controller, and the 4 MiB working-set hypothesis above is exactly the kind of thing that gets worse, not better, under contention.
- `bytes.IndexByte` (NEON, per row) at 13.28 ns/row in the no-table floor against the SWAR scan's 11.67 in a differently-shaped loop is *consistent* with `04-asm-kernels.md`'s H2 result from an independent direction, but the two loops are not the same shape and no verdict is drawn from the pair.
- benhoyt r10's technique of computing the hash from the masked name word *while* scanning for the separator was read but not measured: it fuses two passes over the name into one, and our SWAR scan currently skips 8 bytes at a time and then hashes separately. Whether fusing beats skipping is unmeasured and is a `go-v2-kernels`-sized question.
- The 10k synthetic key set's perfect 8-byte prefix separation is a property of the synthesiser, not of real data. If a future ticket needs a realistic 10k set, upstream's `CreateMeasurements3` generator is the source to port.
- No probe here ran on AC power, so every figure is provisional by rule and the ORDERINGS are the trustworthy part.
