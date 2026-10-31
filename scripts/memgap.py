#!/usr/bin/env python3
"""Memories の応答の大きさと、次の要求までの時間の関係を見る。usage: memgap.py <file> [T上限秒]"""
import sys, collections
f = sys.argv[1]; lim = float(sys.argv[2]) if len(sys.argv) > 2 else 25
per = collections.defaultdict(list); t0 = None
for line in open(f, errors='replace'):
    r = dict(p.split(':', 1) for p in line.rstrip('\n').split('\t') if ':' in p)
    try:
        e = float(r['msec']); rt = float(r['reqtime'])
    except Exception:
        continue
    if t0 is None or e - rt < t0:
        t0 = e - rt
    per[r.get('ip')].append((e - rt, e, r['uri'], int(r.get('bytes', 0))))
bk = collections.defaultdict(list)
for ip, v in per.items():
    v.sort()
    for a, b in zip(v, v[1:]):
        if a[2].startswith('/api/memories?') and a[0] - t0 < lim and b[2].startswith('/api/images/'):
            gap = b[0] - a[1]
            if gap >= 0:
                bk[min(a[3] // 10000, 12)].append(gap)
print('size(KB) count  p50ms  mean')
for k in sorted(bk):
    g = sorted(bk[k]); n = len(g)
    print(f'{k*10:6d}-  {n:6d} {1000*g[n//2]:6.1f} {1000*sum(g)/n:6.1f}')
