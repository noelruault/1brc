# e32-instructions.py — the /usr/bin/time -l instruction counter per run, which is what settles
# "the profiler costs wall clock and no work" without deriving it from user CPU.
import re, sys
LOG = sys.argv[1] if len(sys.argv) > 1 else '1brc/bench/2026-09-02T004137Z-go-opt-round-3-gap-instrument-pass.txt'
rows = []
for m in re.finditer(r'=== round (\d+) (-phases|-cpuprofile) ===(.*?)(?=\n=== round|\nload at end)', open(LOG).read(), re.S):
    rnd, kind, body = m.groups()
    rows.append((int(rnd), kind, int(re.search(r'(\d+)\s+instructions retired', body).group(1))))
for r in rows:
    print(f"round {r[0]} {r[1]:<12} {r[2]:,} instructions")
ph = [r[2] for r in rows if r[1] == '-phases' and r[0] > 0]
pr = [r[2] for r in rows if r[1] == '-cpuprofile' and r[0] > 0]
allr = [r[2] for r in rows]
print(f"\n-phases rounds 1-3 mean {sum(ph)/len(ph):,.0f} ; -cpuprofile rounds 1-3 mean {sum(pr)/len(pr):,.0f}")
print(f"profiled vs unprofiled: {(sum(pr)/len(pr))/(sum(ph)/len(ph))-1:+.3%}  (against +13.74% of wall)")
print(f"spread over all eight runs: {(max(allr)-min(allr))/min(allr):.3%}")
print(f"instructions per row, 1e9 rows: {sum(ph)/len(ph)/1e9:.1f}")
