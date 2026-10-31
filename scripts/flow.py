#!/usr/bin/env python3
"""IP ごとのリクエスト列を出す。usage: flow.py <file> [ip]"""
import sys, re, collections
f = sys.argv[1]
ip = sys.argv[2] if len(sys.argv) > 2 else None
rows = collections.defaultdict(list)
for line in open(f, errors='replace'):
    d = dict(p.split(':', 1) for p in line.rstrip('\n').split('\t') if ':' in p)
    rows[d.get('ip', '')].append(d)
if ip is None:
    for k, v in sorted(rows.items(), key=lambda kv: -len(kv[1]))[:60]:
        c = collections.Counter(re.sub(r'[0-9a-f-]{36}', ':id', r['uri'].split('?')[0]) for r in v)
        print(k, len(v), c.most_common(6))
else:
    v = rows[ip]
    v.sort(key=lambda r: float(r['msec']) - float(r['reqtime']))
    t0 = float(v[0]['msec']) - float(v[0]['reqtime'])
    for r in v:
        s = float(r['msec']) - float(r['reqtime'])
        print(f"{s-t0:8.3f} {float(r['reqtime']):7.3f} {r['status']} {r['method']:6} {r['uri'][:110]} req={r['reqbytes']} res={r['bytes']}")
