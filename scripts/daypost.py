#!/usr/bin/env python3
"""リージョンの投稿期間ごとの投稿数と、通知から投稿までの時間を出す。usage: daypost.py <dir> <host>"""
import sys, collections
d, h = sys.argv[1], sys.argv[2]
rows = []
for line in open(f'{d}/{h}.ltsv', errors='replace'):
    r = dict(p.split(':', 1) for p in line.rstrip('\n').split('\t') if ':' in p)
    try:
        e = float(r['msec']); rt = float(r['reqtime'])
    except Exception:
        continue
    rows.append((e - rt, e, r['uri'], r['method'], r['status'], r.get('ip')))
rows.sort()
t0 = rows[0][0]
posts = [(s, e, st, ip) for s, e, u, m, st, ip in rows if m == 'POST' and u == '/api/posts']
sse = [(s, e, st, ip) for s, e, u, m, st, ip in rows if u == '/api/notifications']
# 通知の完了時刻の塊を投稿期間の始まりと見なす
ends = sorted(e for s, e, st, ip in sse if e - s > 0.5)
starts = []
for e in ends:
    if not starts or e - starts[-1] > 4:
        starts.append(e)
for i, st in enumerate(starts):
    en = starts[i + 1] if i + 1 < len(starts) else st + 15
    p = [x for x in posts if st <= x[0] < en]
    late = sorted(x[1] - st for x in p)
    dur = sorted(x[1] - x[0] for x in p)
    ok = sum(1 for x in p if x[2].startswith('2'))
    n = len(p)
    print(f'day{i+1} t={st-t0:6.1f} posts={n:4d} ok={ok:4d} finish_p50={late[n//2] if n else 0:5.2f} max={late[-1] if n else 0:5.2f} postdur_p50={dur[n//2] if n else 0:5.3f} p90={dur[int(n*.9)] if n else 0:5.3f}')
