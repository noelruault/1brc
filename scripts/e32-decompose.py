import re, sys, statistics as st
# Defaults to the committed raw log so every figure below is re-derivable from the repo alone.
LOG = sys.argv[1] if len(sys.argv) > 1 else '1brc/bench/2026-09-02T004137Z-go-opt-round-3-gap-instrument-pass.txt'
log = open(LOG).read()
CORES = 15
# blocks: "=== round N -phases ===" ... phases lines ... "R real U user S sys"
blocks = re.findall(
    r'=== round (\d+) (-phases|-cpuprofile) ===(.*?)(?=\n=== round|\nload at end)', log, re.S)
rows = []
for rnd, kind, body in blocks:
    m = re.search(r'([\d.]+) real\s+([\d.]+) user\s+([\d.]+) sys', body)
    if not m: continue
    real, user, sys_ = map(float, m.groups())
    d = dict(round=int(rnd), kind=kind, real=real, user=user, sys=sys_)
    p = re.search(r'read=([\d.]+)s fold=([\d.]+)s', body)
    if p: d['read'], d['fold'] = float(p.group(1)), float(p.group(2))
    w = re.search(r'max=([\d.]+)s sum=([\d.]+)s skew\(max/min\)=([\d.]+) merge=([\d.]+)(m?)s', body)
    if w:
        d['wmax'] = float(w.group(1)); d['wsum'] = float(w.group(2)); d['skew'] = float(w.group(3))
        d['merge'] = float(w.group(4)) / (1000 if w.group(5) == 'm' else 1)
    rows.append(d)

ph = [r for r in rows if r['kind'] == '-phases' and r['round'] > 0]
pr = [r for r in rows if r['kind'] == '-cpuprofile' and r['round'] > 0]
print('discarded as first-touch:', [ (r['kind'], r['real']) for r in rows if r['round']==0 ])
print()
print('=== -phases rounds 1..3, the shipped default (20 workers, 1 MiB, -parse word, -fold ptr) ===')
for r in ph:
    W, U, S = r['real'], r['user'], r['sys']
    cap = CORES * W
    idle = cap - U - S
    print(f"round {r['round']}: wall {W:.2f}s  user {U:.2f}s  sys {S:.2f}s  read {r['read']:.3f}s  fold {r['fold']:.3f}s  wsum {r['wsum']:.3f}s  skew {r['skew']:.3f}  merge {r['merge']*1000:.2f}ms")
    print(f"          capacity {cap:.3f} core-s = user {U/cap*100:.2f}% + sys {S/cap*100:.2f}% + IDLE {idle/cap*100:.2f}%   (idle {idle:.3f} core-s = {idle/CORES:.4f}s of wall)")
    print(f"          compute floor user/{CORES} = {U/CORES:.4f}s; wall above it = {(W-U/CORES)/W*100:.2f}% ; identity check (sys+idle)/capacity = {(S+idle)/cap*100:.2f}%")
    print(f"          workers blocked in pread at any instant = read/wall = {r['read']/W:.2f} of 20 ; in fold = {r['fold']/W:.2f} ; cores busy = (user+sys)/wall = {(U+S)/W:.2f} of 15")
    print(f"          fold wall {r['fold']:.3f}s vs total user CPU {U:.2f}s -> at least {r['fold']-U:.3f} core-s of fold time is descheduled ({(r['fold']-U)/r['fold']*100:.2f}% of fold)")
    print(f"          outside the worker phase: wall - MAX worker wall = {W - r['wmax']:+.4f}s, against /usr/bin/time's 0.01 s wall resolution; the merge is instrumented directly at {r['merge']*1000:.2f}ms")
    print()

def mean(k, rs): return st.mean(r[k] for r in rs)
W, U, S = mean('real', ph), mean('user', ph), mean('sys', ph)
R, F = mean('read', ph), mean('fold', ph)
cap = CORES * W; idle = cap - U - S
print('=== means over rounds 1..3 ===')
print(f"wall {W:.4f}s  user {U:.4f}s  sys {S:.4f}s  read {R:.4f}s  fold {F:.4f}s")
print(f"compute floor {U/CORES:.4f}s ; wall above the floor {(W-U/CORES)/W*100:.2f}%")
print(f"  of which system CPU {S/cap*100:.2f} points of wall capacity ({S/CORES:.4f}s of wall)")
print(f"  of which idle cores {idle/cap*100:.2f} points ({idle/CORES:.4f}s of wall, {idle/W:.2f} of 15 cores idle on average)")
print(f"sum: {S/cap*100 + idle/cap*100:.2f}% (identity: capacity = user + sys + idle)")
print(f"kernel copy rate implied by sys: 13795610267 B / {S:.4f}s = {13795610267/S/1e9:.2f} GB/s  [DERIVED]")
print(f"read blocked fraction of worker wall = {R/(20*W)*100:.2f}%")
print(f"mean workers with work to do = 20 - read/wall = {20 - R/W:.2f}")
print()
print('=== -cpuprofile rounds 1..3: what the profiler costs ===')
for r in pr:
    print(f"round {r['round']}: wall {r['real']:.2f}s  user {r['user']:.2f}s  sys {r['sys']:.2f}s")
pW, pU, pS = mean('real', pr), mean('user', pr), mean('sys', pr)
print(f"means: wall {pW:.4f}s ({(pW-W)/W*100:+.2f}% vs the -phases runs)  user {pU:.4f}s ({(pU-U)/U*100:+.2f}%)  sys {pS:.4f}s ({(pS-S)/S*100:+.2f}%)")
print(f"profiled-run idle: {(CORES*pW - pU - pS)/(CORES*pW)*100:.2f}% against {idle/cap*100:.2f}% unprofiled")
