#!/usr/bin/env python3
"""最初の投稿期間までに現れた IP の動きをまとめる。usage: day1.py <dir> <host> [limit秒]"""
import sys, re, collections
d, h = sys.argv[1], sys.argv[2]
lim = float(sys.argv[3]) if len(sys.argv) > 3 else 12
rows = collections.defaultdict(list); t0 = None
for line in open(f'{d}/{h}.ltsv', errors='replace'):
    r = dict(p.split(':', 1) for p in line.rstrip('\n').split('\t') if ':' in p)
    try:
        e = float(r['msec']); rt = float(r['reqtime'])
    except Exception:
        continue
    s = e - rt
    if t0 is None or s < t0:
        t0 = s
    rows[r.get('ip')].append((s, e, r['method'], re.sub(r'[0-9a-f]{8}-[0-9a-f-]{27}', ':id', r['uri'].split('?')[0]), r['status']))
for ip, v in sorted(rows.items(), key=lambda kv: min(x[0] for x in kv[1])):
    v.sort()
    if v[0][0] - t0 > lim:
        continue
    seq = []
    for s, e, m, u, st in v:
        if s - t0 > lim:
            break
        short = {'/api/auth/register': 'REG', '/api/auth/login': 'LOGIN', '/api/posts': 'POST', '/api/notifications': 'SSE', '/api/posting-window/current': 'cur', '/api/friend-requests': 'FR', '/api/follows/:id': 'FOL', '/api/blocks/:id': 'BLK'}.get(u, u.replace('/api/', ''))
        if short.startswith('images') or short.startswith('feeds') or short.startswith('users') or short.startswith('posts/') or short.startswith('memories') or short.startswith('relationships'):
            continue
        seq.append(f'{s-t0:.1f}:{short}' + ('' if st.startswith('2') else f'[{st}]') + (f'({e-s:.1f}s)' if short == 'SSE' else ''))
    print(ip, ' '.join(seq)[:230])
