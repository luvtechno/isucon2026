#!/usr/bin/env python3
"""ユーザー（IP）ごと・投稿期間ごとに、上限を超えた読み取りの割合を見積もる。usage: overcap.py <dir>"""
import sys, glob, re, collections
d = sys.argv[1]
UU = re.compile(r'[0-9a-f]{8}-[0-9a-f-]{27}')
caps = {'feed': 20, 'image': 4, 'post': 8, 'lists': 8, 'profile': 4, 'rm': 5, 'cm': 5}
tot = collections.Counter(); over = collections.Counter(); bytes_over = collections.Counter(); bytes_tot = collections.Counter()
other = collections.Counter()
for f in glob.glob(d + '/*.ltsv'):
    host = f.split('/')[-1].split('.')[0]
    per = collections.defaultdict(list)
    for line in open(f, errors='replace'):
        r = dict(p.split(':', 1) for p in line.rstrip('\n').split('\t') if ':' in p)
        try:
            r['start'] = float(r['msec']) - float(r['reqtime'])
        except Exception:
            continue
        per[r.get('ip')].append(r)
    for ip, rows in per.items():
        rows.sort(key=lambda r: r['start'])
        if sum(1 for r in rows if '/api/feeds/' in r['uri']) == 0:
            continue
        cnt = collections.Counter()
        for r in rows:
            u = UU.sub(':id', r['uri'].split('?')[0]); m = r['method']
            if m == 'POST' and u == '/api/posts':
                cnt = collections.Counter()  # 次の投稿期間
                other['POST /api/posts'] += 1
                continue
            kind = None
            if m == 'GET' and u.startswith('/api/feeds/'): kind = 'feed'
            elif m == 'GET' and u.startswith('/api/images/'): kind = 'image'
            elif m == 'GET' and u == '/api/posts/:id': kind = 'post'
            elif m == 'GET' and (u.endswith('/comments') or u.endswith('/realmojis')): kind = 'lists'
            elif m == 'GET' and u == '/api/users/:id': kind = 'profile'
            elif m == 'POST' and u.endswith('/realmojis'): kind = 'rm'
            elif m == 'POST' and u.endswith('/comments'): kind = 'cm'
            if kind is None:
                other[m + ' ' + u] += 1
                continue
            cnt[kind] += 1
            tot[kind] += 1; bytes_tot[kind] += int(r.get('bytes', 0))
            if cnt[kind] > caps[kind]:
                over[kind] += 1; bytes_over[kind] += int(r.get('bytes', 0))
for k in caps:
    print(f'{k:8} total={tot[k]:7d} over={over[k]:7d} ({100*over[k]/max(1,tot[k]):4.1f}%) MB={bytes_tot[k]/1e6:8.1f} overMB={bytes_over[k]/1e6:8.1f}')
print('others', other.most_common(24))
