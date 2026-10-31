#!/usr/bin/env python3
"""全ノード合計の毎秒要求数と転送量を5秒ごとに出す。usage: total.py <dir>..."""
import sys, glob, collections
for d in sys.argv[1:]:
    c = collections.Counter(); b = collections.Counter(); ib = collections.Counter(); t0 = None; rows = []
    for f in glob.glob(d + '/*.ltsv'):
        for line in open(f, errors='replace'):
            r = dict(p.split(':', 1) for p in line.rstrip('\n').split('\t') if ':' in p)
            try:
                e = float(r['msec']); rt = float(r['reqtime'])
            except Exception:
                continue
            if 'initialize' in r.get('uri', ''):
                t0 = e - rt
            if 'notifications' in r.get('uri', ''):
                continue
            rows.append((e, int(r.get('bytes', 0)), int(r.get('reqbytes', 0))))
    for e, x, y in rows:
        k = int((e - t0) // 5) * 5
        c[k] += 1; b[k] += x; ib[k] += y
    print(d, 'req/s:', ' '.join(f'{k}:{c[k]//5}' for k in sorted(c) if 0 <= k <= 70), 'total', sum(c.values()))
    print('   outMB/s:', ' '.join(f'{k}:{b[k]//5000000}' for k in sorted(c) if 0 <= k <= 70), 'totalGB', sum(b.values()) // 1000000000)
