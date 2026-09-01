# 01 — What the One Billion Row Challenge actually asks

Written from the upstream repository, not from memory. Every rule below cites the file and line it came from; the fetch commands, upstream commit and content hashes are in `01-definition-data.txt`. Source of record: `gunnarmorling/1brc` at commit `db06419`, fetched 2026-09-01.

## The task

Read a UTF-8 text file of `<station name>;<temperature>` lines and emit, per station, the min, mean and max temperature, sorted by station name, on stdout (README.md:26, README.md:43).

Input line format, one measurement per line, `\n` terminated:

```
Hamburg;12.0
Bulawayo;8.9
St. John's;15.2
```

Output is a single line, a Java `TreeMap.toString()`:

```
{Abha=-23.0/18.0/59.2, Abidjan=-16.2/26.0/67.3, Abéché=-10.0/29.4/69.0, ...}
```

That is `{`, then `name=min/mean/max` entries joined by `, `, then `}`, then a newline from `System.out.println` (CalculateAverage_baseline.java:105, README.md:45).

## The binding constraints

| Constraint | Value | Source |
|---|---|---|
| Station name | non-null UTF-8, 1 character min, **100 bytes** max, contains no `;` and no `\n` | README.md:421 |
| Temperature | non-null double in `[-99.9, 99.9]`, **always exactly one fractional digit** | README.md:422, README.md:26 |
| Distinct stations | at most **10,000** | README.md:423 |
| Line ending | `\n` on all platforms | README.md:424 |
| Encoding | UTF-8 | README.md:488 |
| Output rounding | IEEE 754 `roundTowardPositive` | README.md:426 |
| Data-set assumptions | forbidden: any valid name and any distribution must work | README.md:425 |

The name limit is **100 bytes, not 100 characters** — a name may be 50 two-byte characters. A fixed 100-byte name buffer is therefore safe; a 100-*rune* assumption is not.

## Output semantics, derived from the reference implementation

The rules give the rounding *direction*; the exact arithmetic only exists in the reference source. `CalculateAverage_baseline.java` is the authority, and it is more specific than the prose:

- `round(v) = Math.round(v * 10.0) / 10.0` (CalculateAverage_baseline.java:41). Java's `Math.round` is `floor(x + 0.5)`, which is exactly `roundTowardPositive` on ties — so `-2.45 → -2.4`, not `-2.5`. A language whose `round` is round-half-away-from-zero (Go's `math.Round`, C's `round`) **disagrees on every negative tie**.
- min and max are `round(stored double)` (CalculateAverage_baseline.java:39).
- The mean is rounded **twice**: `mean = round( round(sum) / count )` (CalculateAverage_baseline.java:88 then :41). The inner round snaps the accumulated floating-point sum back onto a one-decimal grid before the division; dropping it changes the last digit on some stations.
- Values are printed by Java string concatenation, i.e. `Double.toString`. After `round`, every value is `(long)/10.0`, whose shortest round-tripping decimal has exactly one fractional digit, so the printed form is always `-?\d+\.\d` — *derived* from the source, empirically confirmed below.
- **`-0.0` can never appear.** `Math.round` returns a `long`, so a value rounding to zero becomes `0L`, and `0L/10.0` is positive zero. A Go implementation that computes in float and prints `%.1f` emits `-0.0` for a mean of `-0.04` and diverges. Our reference must aggregate in integer tenths, where this cannot arise.
- Ordering is `TreeMap<String>`, i.e. Java's `String.compareTo`: **UTF-16 code-unit order**, not locale collation and not byte order. For the BMP characters used by the official generator (`Abéché`, `Zürich`) UTF-16 code-unit order and Unicode code-point order agree, so a Go `sort.Strings` over UTF-8 bytes gives the same result. They diverge only for supplementary-plane characters (U+10000 and above), which the official data set does not contain — a real trap for the "arbitrary name" rule, recorded here rather than discovered later.

These five behaviours are **derived by reading the cited Java source**, not confirmed against a running JVM: this machine has only a Java 8 JRE (no `javac`, and the baseline needs records and `nextGaussian(mean, sd)` from Java 17+), so the upstream program cannot be executed here. Installing a JDK to close that gap is recorded as a revive trigger rather than done now.

What *is* measured is the Go side of each divergence, which is the half that can actually break our implementation. `01-definition-data.txt` holds the output of `1brc/code/gen/tenths_test.go`, which pins: Go's `math.Round` disagreeing with Java's `Math.round` on every negative tie, `fmt.Sprintf("%.1f", -0.04)` emitting `-0.0`, and Go's byte-order string sort disagreeing with Java's UTF-16 order above U+FFFF. Those three are the traps; the reference implementation is written to avoid all three by construction.

## How the official evaluation was run

Hetzner AX161: 32-core AMD EPYC 7502P (Zen2), 128 GB RAM, Fedora 39; the program is pinned to **8 cores** with `numactl --physcpubind=0-7` and the file is served from a **RAM disk**, so I/O is not part of the measurement (README.md:458, evaluate.sh:186).

The measurement is `hyperfine --warmup 0 --runs 10` (evaluate.sh:37, evaluate.sh:178) followed by a **trimmed mean**: sort the ten times, drop the fastest and the slowest, average the remaining eight (evaluate.sh:211).

**The README prose is stale on this point.** README.md:464-466 says "run five times in a row. The slowest and the fastest runs are discarded. The mean value of the remaining three runs is the result" — the script says ten runs and eight kept. The comment above the `jq` line (evaluate.sh:209) still repeats the "remaining three" wording while the code below it does not implement it. The script is the ground truth for what produced the leaderboard.

## The leaderboard, and why our clock is not comparable to it

Top of the published results (README.md:57-60), all on the 8-core AX161:

| # | Time | Author | Technique |
|---|---|---|---|
| 1 | 1.535 s | thomaswue / merykitty / mukel | GraalVM native binary, `Unsafe` |
| 2 | 1.587 s | artsiomkorzun | GraalVM native binary, `Unsafe` |
| 3 | 1.608 s | jerrinot | GraalVM native binary, `Unsafe` |
| 4 | 1.880 s | serkan-ozal | `Unsafe`, no native image |
| 5 | 1.921 s | abeobk | GraalVM native binary, `Unsafe` |

Every entry in the top five is an AOT-compiled native binary using `Unsafe` for unchecked memory access — i.e. the winning Java programs converged on being C programs. That is the useful signal for a Go attempt: the win is in memory access strategy and tokenisation, not in language runtime tricks.

These numbers are **facts about a Zen2 EPYC restricted to 8 cores**. Ours will be an Apple M5 Pro with 15 logical cores. Upstream's own FAQ makes the point (README.md:503): results are only comparable on the same machine. We record both and compare *techniques*, never clocks.

## The data set

`CreateMeasurements.java` picks a station uniformly from a hardcoded list of **413** stations (verified by count, see data companion) and draws `nextGaussian(stationMean, 10.0)` rounded to one decimal (CreateMeasurements.java:28-31, :497-500). The station means come from a Wikipedia table of cities by average temperature (CreateMeasurements.java:54).

Two properties matter and are *not* guaranteed by that generator:

- It does **not clamp** to `[-99.9, 99.9]`. With σ=10 the bound is beyond 6σ from every station mean, so it is satisfied in practice but not by construction. Our generator clamps, so the file it writes provably satisfies README.md:422.
- It uses `ThreadLocalRandom`, so **the official file is not reproducible**. Ours is seeded and records the seed, because in this study a number nobody can re-derive is worthless.

There is a second official generator, `CreateMeasurements3.java`, which synthesises **10,000** station names up to `MAX_NAME_LEN = 100` with σ=7.0 (CreateMeasurements3.java:31-32, :54). It backs the *test suite's* worst-case key-set, not the leaderboard run — `evaluate.sh:36` benchmarks `measurements_1B.txt`, the 413-station file. So the 10k variant is a **correctness and hash-distribution** stressor for us, not a headline benchmark. It is worth generating for exactly that reason: a hash map tuned to 413 keys that collapses at 10,000 keys is a bug the leaderboard file would never reveal.

## Our target, restated for this machine

Process 1,000,000,000 rows of `measurements-1b.txt` in **under 1.0 s wall clock**, warm page cache, on: Apple M5 Pro, 15 logical cores (`hw.perflevel0` "Super" ×5, `hw.perflevel1` "Performance" ×10), 24 GB RAM, macOS 26.5.2 / Darwin 25.5.0 arm64, go1.27.0. Exact `sysctl` output in the data companion.

Restated as a rate: the file is ~13.8 GB, so <1 s means **>13.8 GB/s of sustained parse throughput and ~1 ns per row across all cores**. Whether that is even reachable against this machine's memory bandwidth is the question `env-baseline` measures next; this report only fixes what "correct" and "done" mean.

Success criterion is honest measurement, not the number: an implementation that misses 1.0 s with a profile-backed explanation of what stopped it is a result. An unmeasured claim is not.

## Sources

All fetched 2026-09-01 from `github.com/gunnarmorling/1brc` at commit `db064194be375edc02d6dbcd21268ad40f7e2869`; hashes in `01-definition-data.txt`.

- `README.md` — rules, output format, evaluation method, leaderboard, FAQ
- `evaluate.sh` — the actual benchmark procedure
- `src/main/java/dev/morling/onebrc/CalculateAverage_baseline.java` — authoritative output semantics
- `src/main/java/dev/morling/onebrc/CreateMeasurements.java` — the 413-station data set
- `src/main/java/dev/morling/onebrc/CreateMeasurements3.java` — the 10k-station variant
- `create_measurements.sh`, `calculate_average_baseline.sh` — invocation

Upstream is Apache-2.0. Nothing in this study copies upstream code; semantics studied and reimplemented from reading are recorded in `LICENSES.md`.
