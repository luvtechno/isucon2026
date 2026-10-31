#!/usr/bin/env python3
"""秒ごとの完了数・投稿の完了数・投稿の同時実行数を出す。usage: secs.py <dir> <from> <to>"""
import sys, glob, collections
d, a, b = sys.argv[1], float(sys.argv[2]), float(sys.argv[3])
rows = []; t0 = None
for f in glob.glob(d + '/*.ltsv'):
    h = f.split('/')[-1].split('.')[0]
    for line in open(f, errors='replace'):
        r = dict(p.split(':', 1) for p in line.rstrip('\n').split('\t') if ':' in p)
        try:
            e = float(r['msec']); rt = float(r['reqtime'])
        except Exception:
            continue
        if 'initialize' in r.get('uri', ''):
            t0 = e - rt
        rows.append((h, e - rt, e, r['method'], r['uri'].split('?')[0], int(r.get('bytes', 0)), int(r.get('reqbytes', 0))))
c = collections.defaultdict(collections.Counter)
for h, s, e, m, u, bb, rb in rows:
    if 'notifications' in u:
        continue
    k = int(e - t0)
    c[k]['all'] += 1
    c[k]['out'] += bb
    if m == 'POST' and u == '/api/posts':
        c[k]['post_' + h] += 1
        c[k]['in'] += rb
    if u.startswith('/api/memories?'):
        c[k]['mem'] += 1
inflight = collections.Counter()
for h, s, e, m, u, bb, rb in rows:
    if m == 'POST' and u == '/api/posts':
        for t in range(int(s - t0), int(e - t0) + 1):
            inflight[t] += 1
print(' t   all   mem  outMB  inMB  postA postE postU  inflightPOST')
for k in range(int(a), int(b)):
    x = c[k]
    print(f"{k:3d} {x['all']:5d} {x['mem']:5d} {x['out']/1e6:6.0f} {x['in']/1e6:5.0f} {x['post_asia1']:6d} {x['post_europe1']:5d} {x['post_america1']:5d} {inflight[k]:6d}")
