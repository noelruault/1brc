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
