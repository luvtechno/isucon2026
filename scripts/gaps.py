#!/usr/bin/env python3
"""同じ IP の連続するリクエストの間（前の応答の完了から次の要求の到着まで）を時間帯ごとに出す。
ここが広がるなら、律速はアプリの外（ベンチマーカーの処理待ち）にある。usage: gaps.py <dir>"""
import sys, glob, collections
d = sys.argv[1]
rows = collections.defaultdict(list); t0 = None
for f in glob.glob(d + '/*.ltsv'):
    host = f.split('/')[-1].split('.')[0]
    for line in open(f, errors='replace'):
        r = dict(p.split(':', 1) for p in line.rstrip('\n').split('\t') if ':' in p)
        try:
            end = float(r['msec']); rt = float(r['reqtime'])
        except Exception:
            continue
        uri = r.get('uri', '')
        if 'initialize' in uri:
            t0 = end - rt
        if 'notifications' in uri:
            continue
        rows[(host, r.get('ip'))].append((end - rt, end))
buckets = collections.defaultdict(lambda: collections.defaultdict(list))
for (host, ip), v in rows.items():
    v.sort()
    for a, b in zip(v, v[1:]):
        gap = b[0] - a[1]
        if gap >= 0:
            buckets[host][int((b[0] - t0) // 5) * 5].append(gap)
hosts = sorted(buckets)
ts = sorted({t for h in hosts for t in buckets[h]})
print('time ' + ''.join(f'| {h:9} n  p50ms  p90ms ' for h in hosts))
for t in ts:
    line = f'{t:4d} '
    for h in hosts:
        g = sorted(buckets[h].get(t, [0]))
        n = len(g)
        line += f'| {n:9d} {1000*g[n//2]:6.1f} {1000*g[int(n*.9)]:6.1f} '
    print(line)
