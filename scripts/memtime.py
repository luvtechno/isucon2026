#!/usr/bin/env python3
"""Memories（月）の完了数を5秒ごとにノード別に出す。usage: memtime.py <dir>..."""
import sys, glob, collections
for d in sys.argv[1:]:
    per = collections.defaultdict(collections.Counter); t0 = None; rows = []
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
            if r.get('uri', '').startswith('/api/memories?'):
                rows.append((h, e))
    for h, e in rows:
        per[h][int((e - t0) // 5) * 5] += 1
    print(d)
    for h in sorted(per):
        print(f'  {h:9}', ' '.join(f'{k}:{per[h][k]//5}' for k in sorted(per[h]) if k <= 65), 'total', sum(per[h].values()))
