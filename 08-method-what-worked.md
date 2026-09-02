# 08 — Method: what actually worked

Notes for anyone (human or agent) running a similar investigation. These are the practices that caught real errors in THIS study, not general advice: every section is here because it changed an outcome, and every number in it is re-derivable from the command recorded in [`08-method-what-worked-data.txt`](08-method-what-worked-data.txt). The casualty lists these were built from are [`CORRECTIONS.md`](CORRECTIONS.md) (12 entries), [`docs/1brc/review.md`](../docs/1brc/review.md) (18 groups) and [`07-experiment-ledger.md`](07-experiment-ledger.md) (34 rows). Those three counts are as of this report's own commit; `final-dod` ran later in the same branch and took them to **13, 19 and 36** by adding C13, its own review line, and E-35/E-36. Nothing below changes: the sections are grounded in specific rows and specific defects, not in the totals.

The one number that frames the rest: **18 groups were reviewed and 18 came back REPAIRED. Not one was CLEAN.** Fifteen of those eighteen lines use the word *published* to describe at least one defect they repaired. The dominant failure mode of this study was never a wrong program; it was a wrong sentence about a right program, and the practices below are almost all aimed at that.

## The harness refuses; recording is not refusing

`lib-provenance.sh` began as a header writer: it stamped power source, load and configuration onto every benchmark file. E-19 is the row where that turned out to be worthless. Two 1b measurements were voided in ten minutes, and the header had faithfully recorded the state that invalidated both of them: a previous cycle killed mid-benchmark had left its 17-minute invocation running at PPID 1 and the new runs landed inside its arms; then `pmset` flipped to AC and macOS answered the charger with `backupd`, `mdbulkimport` and a WorkflowKit burst, one-minute load **12.79** on 15 cores, the same binary going **1.56 s to 4.54-5.06 s** and **18.56 s to 34.37-38.52 s of user CPU for byte-identical work**.

The fix is that the harness now gates what it used to describe: an exclusive lock (atomic `mkdir` holding the owner's pid, cleared when that pid is dead, so a killed cycle cannot park every later one) refuses a second measurement with status 3, and `require_quiet` waits a busy machine out 15 s at a time up to 180 s before refusing with status 4 and naming the busiest processes. Forcing past it stamps `NOT QUIET`, which joins `NOT A VERDICT` and `NOT SLOT-CORRECTED` as the three ways this study's output says out loud that it should not be quoted.

**Transfers as:** any provenance field that describes a condition under which the number is invalid should be a precondition instead of a field. If the header can tell that the run is worthless, the header should have prevented the run.

## Ask a benchmark to rank N copies of one thing before you believe it about N different things

E-16 is the cheapest high-value experiment in the study: the same binary, the same flags, named eight times in one hyperfine invocation. It measured **1.660, 1.690, 1.745, 1.772, 1.796, 1.811, 1.841, 2.010 s**, monotonically increasing, **+21.08% end to end**, with user CPU rising **+16.65%** for provably identical work. Every verdict in the queue at that point was smaller than the spread of the null.

It was run because two contradictory results (the same flag at −8.64% and at +12.01%) could otherwise have been argued about for a whole cycle. It cost 90 seconds. The fix that followed, E-17, is a 20 s `--prepare` sleep before every timed run at 1b plus the incumbent named **first and last**, which took the bracket spread to **2.54%** and user CPU to a **0.69%** spread.

**Transfers as:** a null experiment is the first thing you run against a new measurement harness, not the thing you run after it embarrasses you. And "the incumbent is arm 1" is not a control, because first is the most flattered slot there is; the incumbent has to appear at both ends or the invocation cannot judge the one arm everything else is measured against.

## A bracket is a refusal, not a correction factor

E-18 did the reasonable-looking thing with E-16's measured drift: subtract 0.0302 s per slot from a contaminated invocation and recover its six verdicts arithmetically. E-23 re-measured them properly instead. The derived `+19.18%` for `-split cursor` came back at **+0.06%**, and five of that invocation's six margins vanished outright, a 21.00% kill and an 11.40% keep among them. A second contamination the same day put its entire 27.37% excess in system time with user CPU flat and unordered, a shape no per-slot constant describes at all.

So the rule the study ended with is a refusal: the incumbent is named first and last, and if the bracket spread exceeds **3%**, no arm in that invocation may be quoted at all. A contaminated invocation is re-run, never repaired.

**Transfers as:** when you can characterise a confound well enough to model it, that is a reason to *detect* it, not a licence to subtract it. The model of the confound is one more unvalidated claim standing between you and the number.

## Two ways a measurement fails to transfer, and they are different

**Scale (E-09).** Iterating on the 100m file is ten times cheaper and does not rank arms: **seven arms, seven disagreements.** Four inverted (parse, buffer size, table size, `F_NOCACHE`) and three vanished into overlapping ranges, with mmap **5.6× slower at 1b and marginally faster at 100m**. The mechanism is not mysterious (1.4 GB is 5.8% of RAM and nothing is I/O bound; 13.8 GB is 53.5% and everything is) but the size of the effect earned a binding rule: `experiment.sh` refuses any file but 1b unless `--mechanism-only` is passed, and then stamps `NOT A VERDICT`.

**Baseline shape (E-11).** `04-asm-kernels.md` measured the batch tokenizer at **−40.4%** and the binary measured it at **+10.4%** end to end, a **50-point swing**. The microbenchmark's baseline was a single-needle staged tokenizer writing a token stream; the binary's real arm is a dual-needle scan consuming the row in place. The −40.4% was a true measurement against a program this study does not contain.

**Transfers as:** before quoting a delta forward, name the baseline it was measured against and check that the system you are about to apply it to contains that baseline. E-09 says a smaller input does not rank arms; E-11 says a differently-shaped baseline does not either, and they fail independently.

## Mutate the decision the cycle just made, not the arithmetic

Mutation testing found real gaps in every round it ran, and the pattern in the survivors is sharp: the surviving mutant is repeatedly a **semantic choice made deliberately earlier in the same cycle and left with no test on it**. The first-`;`-versus-last-`;` question shipped three separate times, most recently in `TokenizeBatch`, and every agreement test passed because no key set the study can generate produces a station name containing `;`.

Four more shapes, each of which cost a cycle:

- **A guard can survive because a SIBLING guard masks it.** `pendingSep < 0` and `pendingSep+next != end` each caught the other's mutant, so both looked tested and neither was. Delete the redundant one and re-mutate; the survivor tells you which check is load-bearing.
- **Mutate the BOUNDARY, not the branch.** `long := len(name) > 8` survived a full suite as `> 9`, because the 413-station set's only 8-byte-prefix collision (`Alexandra`/`Alexandria`) has differing lengths and is separated by `nlen` instead. No generated file exercises the key compare at nine bytes.
- **A test that overrides a default leaves the default unpinned.** Both preflight defaults survived until they were asserted directly; a mutant setting the load threshold to 999 disables the gate for every real run while every self-test still passes.
- **A harness must BUILD before it tests**, or a mutant that does not compile is scored as caught. Two guard mutants read as caught here for exactly that reason and both had survived.

The counterpart is knowing what a survivor means. A surviving mutant is one of three things and they need different answers: a real test gap (fix the test), an **equivalent** mutant (a no-op or a slowdown no behavioural test can see: discard it, do not score it), or unkillable by construction (prove that by measurement and write the reason at the code, not a test that cannot exist). Scoring the last two as failures is how a mutation count stops meaning anything.

`go test -race` earned its place in the gate the same way. It was added the moment a second goroutine touched shared memory, and mutation testing priced it in one mutant: releasing a double buffer back to its reader BEFORE copying the partial row out of it passed every byte-compare in the suite and only `-race` caught it. 5.9 s.

## The review gate belongs inside the cycle that built the thing

18 groups, 18 REPAIRED, **0 CLEAN**. If review had been a separate queue, this study would have carried an eighteen-deep backlog of tickets whose whole content was context the building cycle already held.

What the gate is actually good at is narrow and worth naming: **the handoff block is read as an INDEX and never as evidence**, and a claim-versus-diff mismatch is the highest-signal finding available. The reviews that found the most were the ones auditing a docs-only or measurement-only group, where there is no code to hide behind and every defect is therefore a published claim: `go-opt-round-3-item17` (four wrong claims, no code changed), `go-opt-round-3-gap` (a check that could not fail), `go-opt-round-3-park` (six defects, two of them a statistic computed over a set nobody enumerated).

**Transfers as:** a measurement-only or docs-only cycle is not a low-risk cycle. It is the highest-risk kind, because its entire output is claims and none of them are executed by anything.

## The checks that caught the most published defects, in order of cheapness

These are the ones that actually fired, ranked by what they cost to run:

1. **Grep for the number before writing "no earlier report records X".** A novelty claim is the cheapest thing in a report to check and the most embarrassing to get wrong. One cycle published that the Super/Performance core split was unrecorded; three reports record it, one of them the definition.
2. **Evaluate every derived figure in a shell and paste the output.** Arithmetic written in prose is never re-derived by anyone. Three wrong figures shipped in one cycle: a cost formula, its own worked example, and a count of ledger rows that `grep -o 'Verdict: [A-Za-z-]*' | sort | uniq -c` settles in one run.
3. **ENUMERATE the set before computing a statistic over it.** This is the error that survives every arithmetic check. A reproduction claim quoted eight readings and a 0.48% spread; the set had nine readings and a 0.68% spread, because one invocation of five was never listed. A spread whose min and max cannot each be named is not measured.
4. **Cite a line number by grepping for the thing you are citing.** Two of four citations in one backlog split were wrong, both caught by `sed -n '<n>p' <file> | grep -c '<the claim>'` in the seconds before the commit. A wrong line number is worse than none: the next cycle reads it as the contract.
5. **Grep every "measured" sentence for its raw output in the data companion before pushing.** One cycle published a control run, a `PARKED.md` entry and a margin that existed in no committed file.
6. **Read the call site before proposing that a check be replaced by a cheaper equivalent.** "Validate from the integer instead of the bytes" reached the top of the queue while the call site already ran `inRange` beside `validTemp` (`code/go/table.go:276` today, the `parseBranchless` arm); the two are not redundant, because the byte check is what establishes that the bytes were a temperature at all, and the swap would have accepted malformed rows.
7. **Do not write a worked example from reasoning.** Three published examples were wrong in one cycle, all of them illustrations of guards that do reject the input, just not for the reason claimed. The one-guard-at-a-time sweep that attributes them correctly is the evidence the mutation table should have carried in the first place.

## An arithmetic identity is the most convincing thing you can write that cannot fail

`wall × cores = user + sys + idle` decomposes the gap exactly, and with `idle` DEFINED as the remainder it agrees to the digit whatever the inputs are. The handoff had already called it evidence. When a derivation "reproduces perfectly", ask what input would make it disagree; if none exists, go find the check that CAN fail. Here that was the instrumentation closing on worker wall (`sum(worker wall)/(read+fold)` = 1.0008-1.0018), and mutating it proved it discriminates.

The same pass's second mutant paid for itself twice: dropping the round-0 outlier exclusion moved the headline **19.47% to 24.40%** AND changed the shape of the damage (all idle, compute floor flat to 0.0001 s), which turned into an independent corroboration of the verdict.

## Corrections have to reach the backlog, not only the ledger

`CORRECTIONS.md` worked: 12 entries, each naming the claim, every site that published it, what disproved it and the correcting commit. What it did not do on its own is reach the line the next cycle builds from. C7 corrected item 16's mechanism at the ledger, at the board row and at E-21, and one cycle later `go-opt-round-2-batch`'s backlog line still read the falsified version.

**Transfers as:** "corrected at EVERY site that publishes it" has to include `backlog.md` and `spec.md`, because those are the two files a cycle reads BEFORE it reads any report.

A second correction discipline is subtler and cost more: **every DERIVED figure is a hostage to the baseline it came from.** E-25 moved user CPU by 6.33% and silently invalidated a 17.6% ceiling and every profile share published beside it. And a derived ceiling that survives re-derivation is not thereby confirmed: H-14's ceiling was recomputed four times (25% → 17.6% → 19.1% → 20.4%), each time correctly, was promoted to "the only item that can reach the target", and then measured at **+0.57% of wall**. The arithmetic was never wrong. Three re-derivations were three chances nobody took to price the assumption underneath it.

## Park with a quoted test, then test the trigger against the world

`PARKED.md`'s seven fields make a parked idea revivable, but two habits are what made them work here.

**Quote the pre-registered test; never restate it.** The queue already carried item 18's four-slot arm, its "anything over 5% would be a surprise" bar, its second channel and the competing reading the arm exists to distinguish. The register wrote its own arm and its own keep rule instead, and a future cycle would have run a different experiment against a different bar without anyone deciding to change either.

**Fire the triggers against the world rather than reasoning about them.** Two fired in one cheap pass: `memequal` at 8.81-9.87% of compute satisfied P-03's "a measurable share", and the "a Go release exposes the intrinsic" trigger turned out HALF true, because go1.27.0 does ship `simd`/`simd/archsimd` with arm64 support behind `GOEXPERIMENT=simd` while the mask-to-general-register move remains amd64-only. Any claim of the form "the toolchain has no X" needs re-checking per release, and a compile probe needs two controls (the same source on the other GOARCH, and the same file calling a method that DOES exist) or a missing method and an unbuildable package are indistinguishable.

## Refusing to invent a mechanism is a result

The study missed its target: **1.233 s against 1.000 s, +23.3%.** The temptation at the end of a round is to put something on the board where the gap is. Round 3 did not. The separator scan is 41.57-43.64% of compute, both mechanisms aimed at it were measured and neither kept (item 1's fuse CLOSED not-adopted at +0.81%, item 2's batch tokenizer KILLED at +9.8%/+10.4%), and the round's verdict says in as many words that no third mechanism is invented and there is no parked entry against that pot.

That is what `spec.md:61` buys: an honest miss with the ledger intact is a valid research outcome and an unmeasured claim is not. The final state is nine parked entries with pre-registered tests, twelve corrections, and a gap decomposed with no residual into 80.53% user, 7.99% system and 11.48% idle, of which the system half is closed by a kill that has a number on it.

## What did not work

- **Recording without gating.** See E-19. The provenance header described two invalid measurements in perfect detail and prevented neither.
- **Deriving a correction for a contaminated measurement.** E-18's per-slot subtraction produced six numbers, five of which E-23 falsified by re-measuring.
- **The cheap proxy.** The 100m file was supposed to make the optimization loop ten times cheaper. E-09 measured that it ranks nothing, and it survives only as `--mechanism-only` evidence stamped `NOT A VERDICT`.
- **A microbenchmark as a forward-looking recommendation.** `04-asm-kernels.md` handed the batch tokenizer forward as "the shape to build" and named it "the single most load-bearing untested assumption in this report". It was, and it was wrong by 50 points.
- **Treating a derived ceiling as a queue position.** H-14 held the top of the board for two rounds on arithmetic and measured +0.57%.
- **Writing an example from reasoning to illustrate a measurement.** It failed every time it was tried; the sweep that produces the correct example costs minutes.

## The five rules a next study should start with, already paid for

1. Run the null experiment before the first verdict, and re-run it whenever the harness changes.
2. Make every condition that would invalidate a measurement a precondition that refuses, not a field that records.
3. Take deltas inside one bracketed invocation, and treat a wide bracket as a refusal rather than a correction.
4. Mutate the decision the cycle just made, at the boundary, with the build in the loop; and classify each survivor as gap, equivalent, or unkillable before scoring it.
5. Audit the write-up against the raw output, not against your own summary of it, and enumerate any set before computing a statistic over it.
