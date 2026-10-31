#!/usr/bin/env python3
"""全ホストのログから、1つの IP のリクエストを時刻順に出す。usage: flowall.py <dir> <ip>"""
import sys, glob
d, ip = sys.argv[1], sys.argv[2]
rows = []
for f in glob.glob(d + '/*.ltsv'):
    host = f.split('/')[-1].split('.')[0]
    for line in open(f, errors='replace'):
        r = dict(p.split(':', 1) for p in line.rstrip('\n').split('\t') if ':' in p)
        if r.get('ip') == ip:
            r['host'] = host; r['start'] = float(r['msec']) - float(r['reqtime']); rows.append(r)
rows.sort(key=lambda r: r['start'])
t0 = rows[0]['start'] if rows else 0
for r in rows:
    print(f"{r['start']-t0:8.3f} {float(r['reqtime']):7.3f} {r['host']:8} {r.get('cc',''):3} {r['status']} {r['method']:6} {r['uri'][:100]} req={r['reqbytes']} res={r['bytes']}")
