#!/usr/bin/env python3
"""access.ltsv を集計する。usage: alp.py <file>... [--time]"""
import re, sys, collections
args = [a for a in sys.argv[1:] if not a.startswith('--')]
timeline = '--time' in sys.argv
UUID = r'[0-9a-fA-F-]{36}'
rules = [
    (re.compile(r'^/api/images/[^/?]+'), '/api/images/:id'),
    (re.compile(r'^/api/posts/' + UUID + '/comments'), '/api/posts/:id/comments'),
    (re.compile(r'^/api/posts/' + UUID + '/realmojis'), '/api/posts/:id/realmojis'),
    (re.compile(r'^/api/posts/' + UUID), '/api/posts/:id'),
    (re.compile(r'^/api/users/' + UUID), '/api/users/:id'),
    (re.compile(r'^/api/comments/' + UUID), '/api/comments/:id'),
    (re.compile(r'^/api/realmojis/' + UUID), '/api/realmojis/:id'),
    (re.compile(r'^/api/friend-requests/' + UUID + r'/(\w+)'), r'/api/friend-requests/:id/\1'),
    (re.compile(r'^/api/friendships/' + UUID), '/api/friendships/:id'),
    (re.compile(r'^/api/follows/' + UUID), '/api/follows/:id'),
    (re.compile(r'^/api/blocks/' + UUID), '/api/blocks/:id'),
    (re.compile(r'^/api/admin/users/' + UUID + '/certification'), '/api/admin/users/:id/certification'),
    (re.compile(r'^/api/memories/\d{4}-\d{2}-\d{2}'), '/api/memories/:date'),
]
def norm(u):
    u0 = u.split('?')[0]
    for r, t in rules:
        m = r.match(u0)
        if m:
            return m.expand(t)
    return u0
stats = collections.defaultdict(lambda: [0, 0.0, [], 0, collections.Counter(), 0])
buckets = collections.defaultdict(lambda: collections.Counter())
for f in args:
    host = f.split('/')[-1].split('.')[0]
    for line in open(f, errors='replace'):
        d = dict(p.split(':', 1) for p in line.rstrip('\n').split('\t') if ':' in p)
        try:
            t = float(d['reqtime'])
            k = (host, d['method'], norm(d['uri']))
            s = stats[k]
            s[0] += 1; s[1] += t; s[2].append(t); s[3] += int(d.get('bytes', 0)); s[4][d['status']] += 1; s[5] += int(d.get('reqbytes', 0))
            buckets[host][int(float(d['msec'])) // 10] += 1
        except Exception:
            pass
rows = []
for k, s in stats.items():
    v = sorted(s[2])
    p = lambda q: v[min(len(v) - 1, int(len(v) * q))]
    rows.append((s[1], k, s[0], s[1] / s[0], p(.5), p(.99), v[-1], s[3] / s[0], s[5] / s[0], dict(s[4])))
rows.sort(key=lambda r: -r[0])
print(f"{'host':8} {'count':>7} {'sum':>9} {'avg':>7} {'p50':>7} {'p99':>7} {'max':>7} {'bytes':>8} {'req':>7} {'endpoint':42} status")
for r in rows:
    print(f"{r[1][0]:8} {r[2]:7d} {r[0]:9.1f} {r[3]:7.3f} {r[4]:7.3f} {r[5]:7.3f} {r[6]:7.3f} {r[7]:8.0f} {r[8]:7.0f} {r[1][1]+' '+r[1][2]:42} {r[9]}")
if timeline:
    for h, b in buckets.items():
        print(h)
        for t in sorted(b):
            print(' ', t * 10, b[t])
