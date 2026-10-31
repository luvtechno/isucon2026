#!/usr/bin/env python3
"""ノードごとの 5 秒ごとのリクエスト数、転送量(MB)、平均応答時間を出す。usage: rate.py <dir>"""
import sys, glob, collections
d = sys.argv[1]
rows = collections.defaultdict(lambda: collections.defaultdict(lambda: [0, 0, 0, 0.0]))
t0 = None
all_r = []
for f in glob.glob(d + '/*.ltsv'):
    host = f.split('/')[-1].split('.')[0]
    for line in open(f, errors='replace'):
        r = dict(p.split(':', 1) for p in line.rstrip('\n').split('\t') if ':' in p)
        try:
            end = float(r['msec']); rt = float(r['reqtime'])
        except Exception:
            continue
        if 'initialize' in r.get('uri', ''):
            t0 = end - rt
        all_r.append((host, end - rt, end, rt, int(r.get('bytes', 0)), int(r.get('reqbytes', 0)), r.get('uri', '')))
for host, start, end, rt, b, rb, uri in all_r:
    if 'notifications' in uri:
        continue
    k = int((end - t0) // 5) * 5
    s = rows[host][k]
    s[0] += 1; s[1] += b; s[2] += rb; s[3] += rt
hosts = sorted(rows)
ts = sorted({t for h in hosts for t in rows[h]})
print('time  ' + ''.join(f'| {h:8} rps  outMB/s inMB/s  avgms ' for h in hosts))
for t in ts:
    line = f'{t:5d} '
    for h in hosts:
        s = rows[h].get(t, [0, 0, 0, 0.0])
        line += f'| {s[0]/5:12.0f} {s[1]/5e6:7.1f} {s[2]/5e6:6.1f} {1000*s[3]/max(1,s[0]):6.0f} '
    print(line)
