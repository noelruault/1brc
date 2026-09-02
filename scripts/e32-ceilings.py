# e32-ceilings.py — the ceilings and extrapolations E-32 quotes, evaluated rather than written in prose.
# Inputs are the three -phases rounds' means from e32-decompose.py and E-31's off-w15 phases pass.
import re, statistics as st, sys

C = 15
BYTES = 13795610267
# Read the same committed log e32-decompose.py reads: a second file restating numbers computed
# elsewhere drifts silently, which this study has already paid for once in a test helper.
LOG = sys.argv[1] if len(sys.argv) > 1 else '1brc/bench/2026-09-02T004137Z-go-opt-round-3-gap-instrument-pass.txt'
rows = []
for m in re.finditer(r'=== round (\d+) -phases ===(.*?)(?=\n=== round|\nload at end)', open(LOG).read(), re.S):
    rnd, body = m.groups()
    if rnd == '0':
        continue
    t = re.search(r'([\d.]+) real\s+([\d.]+) user\s+([\d.]+) sys', body)
    p = re.search(r'read=([\d.]+)s fold=([\d.]+)s', body)
    rows.append([float(x) for x in t.groups()] + [float(x) for x in p.groups()])
W, U, S, R, F = (round(st.mean(c), 4) for c in zip(*rows))
cap = C * W
idle = cap - U - S
print(f"capacity {cap:.4f} core-s ; user {U:.4f} ; sys {S:.4f} ; idle {idle:.4f}")
print(f"compute floor U/{C} = {U/C:.4f}s ; wall above it {(W-U/C)/W*100:.2f}%")
print(f"perfect-packing wall (U+S)/{C} = {(U+S)/C:.4f}s  -> {((U+S)/C - W)/W*100:.2f}% of wall, and {((U+S)/C-1.0)*100:+.2f}% against the 1.000 s target")
print(f"user CPU needed for a 1.000 s wall at today's overhead structure: {U*1.000/W:.3f}s = {(U*1.000/W-U)/U*100:+.2f}%")
print()
print("--- what one function's whole cost would buy, holding sys and idle FIXED (a ceiling with a named bad assumption) ---")
for name, lo, hi in [("indexDelimAt", 6.09, 6.40), ("parseTempWordFrom", 2.61, 2.89), ("(*table).update", 1.84, 2.06), ("runtime.memequal", 1.29, 1.45)]:
    print(f"  remove {name:<20} {lo:.2f}-{hi:.2f}s of user CPU -> wall {(U-hi+S+idle)/C:.4f}-{(U-lo+S+idle)/C:.4f}s")
print()
print("--- the reader against the device ---")
print(f"aggregate delivered bandwidth {BYTES/W/1e9:.2f} GB/s against env-baseline's measured {BYTES/0.7544/1e9:.2f} GB/s (754.4 ms, 15 uncached preads) = {(BYTES/W)/(BYTES/0.7544)*100:.1f}% of it")
print(f"mean concurrent readers R/W = {R/W:.2f} of 20 ; mean workers with work = {20-R/W:.2f} ; cores busy (U+S)/W = {(U+S)/W:.2f} of {C}")
print(f"fold wall F/W = {F/W:.2f} workers in fold against U/W = {U/W:.2f} cores running user code -> {F/W-U/W:.2f} workers' worth descheduled at any instant ({(F-U)/F*100:.2f}% of fold)")
print()
print("--- worker-count extrapolation, DERIVED from two points (E-31 off-w15, this pass w20), linear ---")
W15, R15 = 1.397, 5.097
run15, run20 = 15 - R15/W15, 20 - R/W
print(f"  15 workers: blocked {R15/W15:.2f}, with work {run15:.2f}")
print(f"  20 workers: blocked {R/W:.2f}, with work {run20:.2f}")
slope = (run20 - run15) / 5
print(f"  slope {slope:.3f} runnable per added worker -> {C} runnable needs {20+(C-run20)/slope:.1f} workers")
