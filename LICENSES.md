# Third-party material studied or derived from

## gunnarmorling/1brc — Apache License 2.0

Upstream: https://github.com/gunnarmorling/1brc, commit `db064194be375edc02d6dbcd21268ad40f7e2869`, fetched 2026-09-01. Copyright 2023 The original authors. Licensed under the Apache License, Version 2.0.

What this study takes from it:

- **The station table in `code/gen/stations.go` is mechanically extracted** from `src/main/java/dev/morling/onebrc/CreateMeasurements.java` — 413 `(name, mean temperature)` pairs. This is a close derivation of an Apache-2.0 file and is the reason this file exists. The underlying data (cities and their average temperatures) comes from Wikipedia's "List of cities by average temperature" via the transformation documented in CreateMeasurements.java:54-75.
- **Output and rounding semantics** in `code/gen/tenths.go` and `code/gen/reference.go` are reimplemented from reading `CalculateAverage_baseline.java`. No code is copied; the arithmetic chain is deliberately reproduced because matching it is the correctness contract.
- **The rules, constraints and evaluation procedure** quoted in `01-definition.md` are cited from `README.md` and `evaluate.sh`.

Not taken: any leaderboard entry's implementation. Techniques from those entries are read for understanding and reimplemented; where an implementation here is closely derived from one, it will be added to this file with the entry's author and file.
