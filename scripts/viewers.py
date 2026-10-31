#!/usr/bin/env python3
"""閲覧役（Memories を繰り返す IP）の数と現れた時刻を出す。usage: viewers.py <dir>"""
import sys, collections
d = sys.argv[1]
for h in ['asia1', 'europe1', 'america1']:
    first = {}; t0 = None; feeds = collections.Counter(); login = collections.Counter(); posts = collections.Counter()
    for line in open(f'{d}/{h}.ltsv', errors='replace'):
        r = dict(p.split(':', 1) for p in line.rstrip('\n').split('\t') if ':' in p)
        try:
            e = float(r['msec'])
        except Exception:
            continue
        if t0 is None or e < t0:
            t0 = e
        u = r['uri']; ip = r.get('ip')
        if u.startswith('/api/feeds/'):
            feeds[ip] += 1
        if u.startswith('/api/auth/login'):
            login[ip] += 1
        if u.startswith('/api/memories'):
            first.setdefault(ip, [e, 0])
            first[ip][1] += 1
    heavy = {k: v for k, v in first.items() if v[1] >= 4 and feeds[k] == 0}
    b = collections.Counter(int((v[0] - t0) // 10) * 10 for v in heavy.values())
    print(h, 'viewers', len(heavy), 'firstseen', sorted(b.items()), 'memsum', sum(v[1] for v in heavy.values()), 'top', sorted((v[1] for v in heavy.values()), reverse=True)[:6], 'active-ips', len(feeds))
