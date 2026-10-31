#!/usr/bin/env python3
"""エンドポイントごとの開始時刻の分布を5秒ごとに出す。usage: timeline.py <dir> [regex]"""
import sys, re, glob, collections
d = sys.argv[1]; pat = re.compile(sys.argv[2]) if len(sys.argv) > 2 else None
rows = []
for f in glob.glob(d + '/*.ltsv'):
    host = f.split('/')[-1].split('.')[0]
    for line in open(f, errors='replace'):
        r = dict(p.split(':', 1) for p in line.rstrip('\n').split('\t') if ':' in p)
        try:
            r['host'] = host; r['end'] = float(r['msec']); r['start'] = r['end'] - float(r['reqtime']); rows.append(r)
        except Exception: pass
t0 = min(r['start'] for r in rows if 'initialize' in r['uri']) if any('initialize' in r['uri'] for r in rows) else min(r['start'] for r in rows)
UU = re.compile(r'[0-9a-f]{8}-[0-9a-f-]{27}')
c = collections.defaultdict(lambda: collections.Counter())
for r in rows:
    k = r['method'] + ' ' + UU.sub(':id', r['uri'].split('?')[0])
    if pat and not pat.search(k): continue
    c[k][int((r['start'] - t0) // 5) * 5] += 1
ts = sorted({t for v in c.values() for t in v})
print(' ' * 44 + ' '.join(f'{t:5d}' for t in ts))
for k, v in sorted(c.items(), key=lambda kv: -sum(kv[1].values())):
    print(f'{k[:43]:44}' + ' '.join(f'{v.get(t, 0):5d}' for t in ts))
