#!/usr/bin/env python3
"""アクティブユーザーの投稿期間ごとの要求数と、その間の時間の使い方を出す。usage: userwin.py <file>"""
import sys, re, collections
f = sys.argv[1]
per = collections.defaultdict(list); t0 = None
for line in open(f, errors='replace'):
    r = dict(p.split(':', 1) for p in line.rstrip('\n').split('\t') if ':' in p)
    try:
        e = float(r['msec']); rt = float(r['reqtime'])
    except Exception:
        continue
    if t0 is None or e - rt < t0:
        t0 = e - rt
    per[r.get('ip')].append((e - rt, e, r['method'], r['uri'].split('?')[0]))
days = collections.defaultdict(list)
for ip, v in per.items():
    v.sort()
    if not any('/api/feeds/' in x[3] for x in v):
        continue
    # 投稿ごとに区切る
    idx = [i for i, x in enumerate(v) if x[2] == 'POST' and x[3] == '/api/posts']
    for a, b in zip(idx, idx[1:] + [len(v)]):
        seg = [x for x in v[a:b] if 'notifications' not in x[3]]
        if len(seg) < 2:
            continue
        t = seg[0][0] - t0
        busy = sum(x[1] - x[0] for x in seg)
        span = seg[-1][1] - seg[0][0]
        writes = sum(1 for x in seg if x[2] != 'GET')
        days[int(t // 9) * 9].append((len(seg), span, busy, writes))
print('t(9s) users  reqs_p50 reqs_p90 span_p50 busy_p50 writes_p50')
for k in sorted(days):
    d = days[k]; n = len(d)
    q = lambda i, p: sorted(x[i] for x in d)[int(n * p)]
    print(f'{k:4d} {n:6d} {q(0,.5):8d} {q(0,.9):8d} {q(1,.5):8.2f} {q(2,.5):8.2f} {q(3,.5):8d}')
