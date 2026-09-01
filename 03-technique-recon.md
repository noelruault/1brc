# 03 — Technique recon: what the top entries do, and what of it survives on arm64

Sources, fetch commands, citations and every probe's raw output: [`03-technique-recon-data.txt`](03-technique-recon-data.txt). Read clean-room per `spec.md:38`: the entries were read for understanding, nothing is pasted, and the two techniques this study will actually depend on were reimplemented and verified here before being written down.

The corpus is the top five 1BRC entries (thomaswue, artsiomkorzun, jerrinot, royvanrijn, merykitty) at upstream commit `db06419`, plus marcelroed/gigatoken for the tokenization angle, plus Go's own arm64 assembly. Their leaderboard times (1.535 s to 2.157 s) are facts about a 32-core EPYC 7502P limited to 8 cores with the file in page cache, and per `spec.md:34` are never compared against our clock. What transfers is technique.

## What every top entry has in common

Four things, and they are the same four in all five sources: **one pass over the bytes with no copies**, **work split into small segments handed out from a shared cursor**, **the whole line parsed by bit arithmetic rather than by scanning characters**, and **a purpose-built open-addressing hash table whose keys are compared 8 bytes at a time**. Nothing above that layer matters — no entry gets a win from a better algorithm, because there is no algorithm here beyond "read, tokenize, accumulate".

Two of their four also carry a Java tax we simply do not pay: every one of the top ten uses `Unsafe` and eight of ten are AOT-compiled GraalVM native binaries, both to escape JVM overhead that Go does not have. thomaswue's most-copied trick — re-exec the process with `--worker` and pipe the child's stdout so the parent exits before the kernel charges it for unmapping 13.8 GB (`thomaswue.java:84-91`, adopted by royvanrijn at `royvanrijn.java:112`) — is a fix for a problem created by mmap, and `02-baseline.md` already ruled mmap out here on measured grounds.

## The one artifact worth more than the code

`royvanrijn.java:35-60` is a 24-line changelog of measured wins, from 62,000 ms down to 1,260 ms, and it is the best available prior on what pays. The full list is in the data companion. The shape of it: chunked reading and a custom hash map are worth ~4x each; SWAR token checks bought 300 ms of 4,200; inlining the hash calculation bought 400 ms; separating the hash array from the entry array bought 900 ms of 2,450, the single largest late win in the list.

Two of its entries are counter-intuitive enough to carry forward as warnings rather than techniques. **"Not using SWAR for EOL: 2850 ms"** (from 3,150) — the clever newline scan was removed and it got faster, which matches our own measurement that newline finding costs 1.9% of the read and therefore has no headroom to win. And **"Replacing branchless code: 2200 ms"** (from 2,450), annotated "sometimes we need to kill the things we love". Neither is explained in the source. Both say the same thing: on this problem, cleverness applied to the wrong stage costs more than it returns, and only measurement tells you which stage is which.

## Technique by technique, with the arm64 verdict

### 1. Segmented work distribution from a shared atomic cursor

All three top entries carve the file into fixed segments and let every thread pull the next one from an atomic counter: 2 MiB in thomaswue (`thomaswue.java:46,57-77`) and artsiomkorzun (`:37`), 4 MiB in jerrinot (`:62,420`). royvanrijn instead runs 2× `availableProcessors()` threads on a static split, commenting "Twice the processors, smoothens things out" (`:82`) and, later, "Can we implement work-stealing? Not sure how..." (`:179`) — he is describing the thing the other three built.

**arm64/macOS equivalent:** identical in shape, with `pread` on disjoint ranges instead of offsets into one mmap, which is what `02-baseline` measured at 754 ms. This machine makes the dynamic version more interesting than it was for them: the 15 cores are split into two performance tiers, 5 "Super" and 10 "Performance" (`hw.perflevel0.name`), so a static 1/15 split finishes unevenly by construction.

**Hypothesis H1:** a shared-cursor split over 2 MiB segments beats a static equal split by ≥5% wall clock on the 1b file, because of the Super/Performance asymmetry. *Test:* implement both in `go-v1-parallel` behind one flag, hyperfine both. *Prediction:* dynamic wins; the static split's slowest shard sets the wall clock and the Super cores idle at the end.

### 2. SWAR delimiter finding — and where it stops paying

The zero-byte trick, `input = word ^ 0x3B3B3B3B3B3B3B3B; (input - 0x0101010101010101) & ~input & 0x8080808080808080`, appears verbatim in thomaswue (`:323-324`), royvanrijn (`:347`) and jerrinot (`:277`, credited to royvanrijn in the source). It sets the high bit of every byte equal to the needle, 8 bytes per operation.

**arm64 equivalent:** SWAR is pure 64-bit integer arithmetic and runs unchanged. The interesting question is what beats it, and arm64 makes that awkward because **there is no `PMOVMSKB`**. The replacement, verified in probe 1: compare 16 bytes with `vceqq_u8`, then narrow the 8×16-bit view right by 4 with `vshrn_n_u16`, which packs each byte's result into one nibble of a 64-bit word; `ctz(mask)>>2` is then the index of the first match. Confirmed at all 16 positions against a scalar reference, with 4 bits set per match. The cheap "is there a match at all" test is `vmaxvq_u8` on the compare result, one instruction.

The stdlib already knows this. `bytes.IndexByte`, `bytes.Count` and `bytes.Equal` are hand-written NEON on arm64 (probe 3): `indexbyte_arm64.s` walks *aligned 32-byte* chunks and builds a 64-bit syndrome at 2 bits per byte using the constant `0x40100401 = (1<<0)+(4<<8)+(16<<16)+(64<<24)` to give each lane a distinct bit. That is the same family as the 4-bit narrowing, at twice the window. It is also why `bytes.Count` ran at memory bandwidth in `02-baseline`.

How often that second window is even needed is a property of the key set, so it was measured rather than guessed. Over the 413 official names: **mean 8.0 bytes, min 3, max 26** ("Las Palmas de Gran Canaria"), with **34.1% longer than 8 bytes and only 1.0% longer than 16**. Rows are drawn uniformly over stations by our generator, so those shares carry over to rows. So SWAR needs a second 8-byte load on about a third of rows, and the 16-byte NEON window covers 99% of rows in one shot — which also means thomaswue's 16-byte hash has a slow path that fires on ~1% of rows, not a negligible one.

**Hypothesis H2:** for finding `;` in a station name, a 16-byte NEON compare beats an 8-byte SWAR word, because SWAR needs a second load on 34.1% of rows where NEON needs a second window on 1.0%. *Test:* microbenchmark both over the real name distribution in `asm-kernels`. *Prediction:* the margin is smaller than the 34.1%/1.0% gap suggests and may go either way, because SWAR's second load is an L1 hit on a line already resident, while NEON pays a fixed transfer from a vector register to a general one on *every* row. SWAR should win outright on the temperature field, where the whole field fits in one 8-byte load. royvanrijn's diary already recorded a version of this result the hard way ("Not using SWAR for EOL: 2850 ms"), which is why the prediction here is deliberately not confident.

### 3. The branchless fixed-point temperature parse

merykitty's parse (`merykitty.java:169-194`) is the most-copied piece of arithmetic in the whole challenge — thomaswue credits "Quan Anh Mai" at `:307`, jerrinot credits merykitty at `:241`, artsiomkorzun names the same constants at `:40-41`. One 8-byte load covers the entire temperature field, and then: bit 4 is set in the ASCII of every digit and clear in `.`, so `ctz(~word & 0x10101000)` locates the decimal point; `(~word << 59) >> 63` is −1 for a leading `-` and 0 otherwise, giving both the sign and a mask that erases it; the digits are shifted to a fixed position and masked to `0x0F000F0F00`; and one multiply by `0x640a0001` sums 100×hundreds + 10×tens + units into bits 32-41, recovered with `>>32 & 0x3FF`.

**Verified, not assumed** (probe 4): reimplemented in Go from that description and run over **every legal temperature — all 1999 values from −99.9 to 99.9 in tenths** — checking both the parsed value and the returned next-line offset. 1999 of 1999 correct. It is branch-free, needs no NEON, and works identically on arm64 because it is integer arithmetic on a 64-bit register.

It carries one hard constraint: it loads 8 bytes from the start of the temperature field, so on the shortest legal field (`0.0\n`, 4 bytes) it over-fetches 4 bytes past the newline. Any chunk boundary or end-of-file handling must guarantee those bytes are readable, or the last line of the file faults. Every entry handles this with a scalar tail path (`merykitty.java:198-218`).

**Hypothesis H3:** this parse costs ≤2 ns/row single-threaded, i.e. under 15% of the 13.9 ns/row core budget from `02-baseline`. *Test:* microbenchmark in `asm-kernels` against a `strconv`-style scalar parse and against a NEON variant. *Prediction:* it wins outright and no NEON version beats it, because the field is 3-5 bytes and a vector load cannot amortise its transfer to a general register over so little work.

### 4. Hash table: 16 bytes of key, no memcmp, and a probe step of 31

Every entry builds the same structure and none uses a language map. thomaswue sizes the table at `1 << 17` for 10,000 cities (`:45`) — a load factor of 7.6%, deliberately wasteful to keep probes short — and computes the hash from **only the first 16 bytes** of the name, as two 8-byte words masked at the delimiter and XORed (`:200-210`). merykitty uses the same `1 << 17` (`:66`) but hashes the first 4 and last 4 bytes (`:244-252`). royvanrijn finishes with `hash ^= hash >> 32` then `>> 17` for "extra entropy" (`:359,380,396`).

Two details are easy to miss and both are load-bearing. Key equality is compared **8 bytes at a time out of the mapped file**, with the final partial word handled by a shift rather than a byte loop (`thomaswue.java:245-266`) — there is no `memcmp` and no string ever materialises. And the collision step is **+31, not +1** (`:249,260`), where merykitty uses +1 (`:253`). A stride of 31 on a 2^17 table trades cache locality for shorter probe chains; nobody measured the two against each other in public.

royvanrijn's diary prices this stage: "Custom hashmap... 4200 ms" (from 4,700), "Inlining hash calculation: 2450 ms" (from 2,850), and the largest single late win in the list, **"Separate hash from entries: 1550 ms"** (from 2,450) — splitting the hash array away from the entry array so a probe touches one cache line of hashes instead of one cache line per candidate entry.

**arm64 equivalent:** unchanged; this is all integer work and pointer arithmetic. In Go it needs `unsafe.Pointer` walks to read 8 bytes at an arbitrary offset without bounds checks, which is `go-recon`'s subject.

**Hypothesis H4:** hashing only the first 16 bytes of the name produces zero *unresolved* collisions on the 413-station set and a manageable number on the 10k set. The measured name distribution above makes this non-trivial: 1.0% of official names exceed 16 bytes, so four of them are hashed on a prefix alone. *Test:* it is a static property of the two key sets — compute it directly, no benchmark needed. **This is the cheapest experiment in the whole study and it gates the design**, because if 16 bytes are not enough the fast path needs a fallback and the fallback changes the loop.

**Hypothesis H5:** separating the hash array from the entry array is worth ≥10% on the 10k-station file and ≈0% on the 413-station one, because 413 entries of hashes fit in L1 either way. *Test:* both layouts, both files, in `go-v1-parallel`.

### 5. Lookup tables instead of shifts, and a hypothesis its author left open

jerrinot masks the name word with two 9-entry tables indexed by the delimiter position (`jerrinot.java:64-86`) rather than computing the mask by shifting, crediting abeobk. Then at `:349-352` he records, in a comment, why he is unsure it is right: *"when pulling just from a single chunk then bit-twiddling is faster than lookup tables / hypothesis: when processing multiple things at once then LOAD latency is partially hidden but when processing just one thing then it's better to keep things local as much as possible? maybe:)"*

That is a real, unresolved question from a top-3 author, stated as a hypothesis. It is worth inheriting rather than re-deriving.

**Hypothesis H6:** on this chip, computing the mask by shift beats a 9-entry lookup table when the loop handles one line at a time, and loses when it interleaves 2+ lines. *Test:* `asm-kernels`, both variants × both loop shapes, four microbenchmarks. *Prediction:* jerrinot is right, and the crossover is visible because an L1 load is ~4 cycles against ~1 for a shift, unless the load latency is hidden.

### 6. Techniques that do not survive the port

- **mmap the whole file.** Universal in the corpus; measured 5-9x slower than `read()` here (`02-baseline.md`), and the fault path does not parallelise even when the data is fully resident. This is the largest single divergence between their design and ours.
- **Re-exec to dodge slow unmap** (`thomaswue.java:84-91`). A consequence of mmap plus JVM teardown. With `pread` there is nothing to unmap; Go's `os.Exit` already skips finalizers.
- **`Unsafe` and GraalVM native images.** Solving JVM problems. Go's equivalent question — how far `unsafe.Pointer` and bounds-check elimination get us — is `go-recon`'s, and it starts from a compiled binary rather than working towards one.
- **gigatoken.** Its public README documents throughput only: 24.53 GB/s tokenizing on two 72-core EPYCs, i.e. 0.17 GB/s per core against the 1.22 GB/s per core our own read floor measured. Different work, and no technique is recoverable from it; `src/lib.rs:1` shows `#![feature(portable_simd)]`, so its kernels are compare-and-bitmask in the same family as the NEON idiom above, and the module sources were not fetchable. It contributes a scale reference and nothing to port. Recorded so nobody re-reads it expecting more.

## The hypothesis queue this hands forward

Six hypotheses, each with a test and a prediction, ordered by what they gate rather than by expected size of win.

| id | hypothesis | test | gates |
|---|---|---|---|
| H4 | 16 bytes of name are enough to hash both key sets without unresolved collisions | static computation over the two key sets, no benchmark | the shape of the hot loop |
| H3 | merykitty's parse costs ≤2 ns/row, and no NEON variant beats it | microbench vs scalar and vs NEON | `asm-kernels` temp-parse winner |
| H2 | 16-byte NEON compare beats 8-byte SWAR for the name scan, and loses for the temperature | microbench over the real name distribution | `asm-kernels` delimiter winner |
| H1 | shared-cursor 2 MiB segments beat a static split by ≥5% on this asymmetric chip | both, behind one flag, hyperfine | `go-v1-parallel` work distribution |
| H5 | splitting the hash array from the entry array is worth ≥10% on 10k stations, ≈0% on 413 | both layouts × both files | table layout |
| H6 | shift-computed masks beat lookup tables one-line-at-a-time, and lose when interleaved | 2 variants × 2 loop shapes | inner-loop structure |

`asm-kernels` owns H2, H3 and H6; `go-v1-parallel` owns H1, H4 and H5. H4 should be answered first regardless of which ticket does it, because it is free and everything else is drawn around its answer.

## Threads left open

- The +31 vs +1 collision stride is unmeasured by anyone in public, on any hardware. It is a two-line change and a plausible few percent.
- `stephenvonworley`, `abeobk`, `serkan_ozal` and `mtopolnik` (ranks 4-9) were not read. jerrinot credits abeobk for the lookup tables and mtopolnik for something at `:389`, so there is at least one more idea in that tier.
- gigatoken's `src/pretokenize/` and `src/input/` are Rust module directories and could not be fetched as raw files. If the tokenization angle turns out to matter, clone the repo rather than fetching file by file.
- Nobody in the corpus measured on Apple silicon at all, so every "this is faster" in their comments is an x86-64 result. The two-tier Super/Performance topology on this chip has no counterpart in their measurements.
- The over-fetch constraint from H3's parse (up to 4 bytes past the final newline) and the 16-byte name window interact at chunk boundaries. Both entries handle it with a scalar tail; whether one tail path can serve both is not worked out here.
