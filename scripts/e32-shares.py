import re, subprocess, statistics as st
USER_CPU = 14.6567   # measured mean user CPU of the three unprofiled -phases runs
funcs = {}
tot = {}
for i in (1, 2, 3):
    out = subprocess.run(['go','tool','pprof','-top','-nodecount=30','1brc/code/go/bin/1brc',f'1brc/bench/2026-09-02T004137Z-go-opt-round-3-gap-cpuprofile-round{i}.pprof'],
                         capture_output=True, text=True).stdout
    total = float(re.search(r'Total samples = ([\d.]+)s', out).group(1))
    flat = {}
    for line in out.splitlines():
        m = re.match(r'\s+([\d.]+)s\s+[\d.]+%\s+[\d.]+%\s+[\d.]+s\s+[\d.]+%\s+(.+)', line)
        if m:
            flat[m.group(2).replace(' (inline)','').strip()] = float(m.group(1))
    sysc = flat.get('syscall.rawsyscalln', 0.0)
    nonsys = total - sysc
    tot[i] = (total, sysc, nonsys)
    for k, v in flat.items():
        if k == 'syscall.rawsyscalln': continue
        funcs.setdefault(k, {})[i] = v / nonsys * 100

print("profile | total samples | syscall.rawsyscalln | non-syscall bucket")
for i, (t, s, n) in tot.items():
    print(f"  {i}     | {t:.2f}s        | {s:.2f}s ({s/t*100:.2f}%)   | {n:.2f}s")
print(f"\nnon-syscall samples mean {st.mean(n for _,_,n in tot.values()):.2f}s against {USER_CPU:.4f}s of measured user CPU")
print("shares are of the NON-syscall samples (E-26's rule); seconds column applies the share to measured user CPU\n")
print(f"{'function':<28} {'p1':>7} {'p2':>7} {'p3':>7}   {'range':>14}   {'x user CPU (s)':>16}")
for k in sorted(funcs, key=lambda k: -st.mean(funcs[k].values())):
    v = funcs[k]
    if st.mean(v.values()) < 0.5: continue
    lo, hi = min(v.values()), max(v.values())
    cols = ' '.join(f"{v.get(i, float('nan')):7.2f}" for i in (1,2,3))
    print(f"{k:<28} {cols}   {lo:5.2f}-{hi:5.2f}%   {lo/100*USER_CPU:5.2f}-{hi/100*USER_CPU:5.2f}")
