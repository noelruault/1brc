# Third-party material studied or derived from

## gunnarmorling/1brc — Apache License 2.0

Upstream: https://github.com/gunnarmorling/1brc, commit `db064194be375edc02d6dbcd21268ad40f7e2869`, fetched 2026-09-01. Copyright 2023 The original authors. Licensed under the Apache License, Version 2.0.

What this study takes from it:

- **The station table in `code/gen/stations.go` is mechanically extracted** from `src/main/java/dev/morling/onebrc/CreateMeasurements.java` — 413 `(name, mean temperature)` pairs. This is a close derivation of an Apache-2.0 file and is the reason this file exists. The underlying data (cities and their average temperatures) comes from Wikipedia's "List of cities by average temperature" via the transformation documented in CreateMeasurements.java:54-75.
- **Output and rounding semantics** in `code/gen/tenths.go` and `code/gen/reference.go` are reimplemented from reading `CalculateAverage_baseline.java`. No code is copied; the arithmetic chain is deliberately reproduced because matching it is the correctness contract.
- **The rules, constraints and evaluation procedure** quoted in `01-definition.md` are cited from `README.md` and `evaluate.sh`.
- **`testdata/upstream-samples/` is twelve input/output pairs copied verbatim** from `src/test/resources/samples/` (564 KB, unmodified). They are the external correctness oracle, run as case zero of `check-correctness.sh`; see `CORRECTIONS.md` C14. Copied rather than reimplemented on purpose: an oracle this study rewrote would not be an external oracle.

Not taken: any leaderboard entry's implementation. Techniques from those entries are read for understanding and reimplemented; where an implementation here is closely derived from one, it will be added to this file with the entry's author and file.

### Leaderboard entries read for `03-technique-recon.md`

All five are files inside the same Apache-2.0 repository and commit: `CalculateAverage_thomaswue.java`, `CalculateAverage_artsiomkorzun.java`, `CalculateAverage_jerrinot.java`, `CalculateAverage_royvanrijn.java`, `CalculateAverage_merykitty.java`. Copyright 2023 The original authors.

Two pieces of arithmetic from them are close derivations and are flagged here in advance of the code that will use them, because the constants are the technique:

- **The branchless fixed-point temperature parse** — `0x10101000` to locate the decimal point, `(~word << 59) >> 63` for the sign, `0x0F000F0F00` to isolate the digits, and the multiplier `0x640a0001` to sum them. Originated by Quan Anh Mai (merykitty) at `CalculateAverage_merykitty.java:169-194`, where the derivation is explained in comments; the same constants appear in thomaswue, jerrinot and artsiomkorzun, each crediting merykitty. This study reimplements the arithmetic in Go from that description, verified exhaustively over all 1999 legal temperatures. The expression is reproduced because reproducing it is the point; the surrounding code is ours.
- **The SWAR zero-byte delimiter find** — `(x - 0x0101010101010101) & ~x & 0x8080808080808080` after XOR with a splatted needle. Present in thomaswue, royvanrijn and jerrinot (the last crediting royvanrijn in the source). This is a widely published bit trick that predates 1BRC (Mycroft's, via *Hacker's Delight*), and it is noted here because these files are where this study read it.

Read but not derived from: their segment/cursor structure, hash-table sizing and probing, and jerrinot's lookup-table masking (credited in his source to abeobk). Where any of those turns into code here, this file gets a line.

## Implementations read for `05-go-techniques.md`

Fetched 2026-09-01; sha256 of every fetched file is in `05-go-techniques-data.txt`.

- **`benhoyt/go-1brc` — MIT** (https://github.com/benhoyt/go-1brc/blob/master/LICENSE.txt), Copyright Ben Hoyt. `r9.go` and `r10.go` were read in full. Nothing is copied. Three ideas are adopted as shapes and reimplemented: a per-shard linearly-probed open-addressing table merged into a map at the end, a hash computed from the first 8 bytes of the name with a full `bytes.Equal` compare resolving collisions, and chunked reads with the trailing partial row carried into the next buffer. `r10.go`'s own header credits its SWAR-and-hash structure to a translation of `CalculateAverage_mtopolnik.java` from the Apache-2.0 upstream. If our table ends up close enough to r9/r10 to count as a derivation rather than a convergence, this line becomes a derivation entry.
- **`shraddhaag/1brc` — NO LICENSE FILE**, so no code from it may be used at all; only the author's published write-up (https://www.bytesizego.com/blog/one-billion-row-challenge-go, by Matt Boyle for ByteSizeGo, 2024-02-19) was read, and only its narrative of what worked. Nothing from that repository is reproduced here in any form.
- **`automataIA/1brc-rs` — MIT** (https://github.com/automataIA/1brc-rs/blob/main/LICENSE). Only `README.md` was read. What it contributes is a published architecture (positional reads over disjoint ranges, one reusable 4 MiB buffer and one local map per worker, nothing shared in the row loop) and its measured numbers on the author's machine. No Rust was read or ported.
