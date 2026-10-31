#!/usr/bin/env python3
"""アクセスログから、活動ごとの得点をおおまかに見積もる（上限は考慮しない）。usage: score.py <dir>"""
import sys, glob, re, collections
d = sys.argv[1]
mult = {'asia1': 1.0, 'europe1': 1.5, 'america1': 1.5}
UU = re.compile(r'[0-9a-f]{8}-[0-9a-f-]{27}')
rules = [
 ('GET /api/feeds/friends', 1), ('GET /api/feeds/public', 1), ('GET /api/feeds/following', 1), ('GET /api/feeds/friends-of-friends', 1),
 ('POST /api/posts', 10), ('GET /api/posts/:id', 1), ('GET /api/posts/:id/comments', 1), ('GET /api/posts/:id/realmojis', 1),
 ('GET /api/users/:id', 1), ('GET /api/notifications', 1), ('GET /api/posts/me/current', 1), ('POST /api/posts/:id/realmojis', 5), ('POST /api/posts/:id/comments', 3),
 ('DELETE /api/realmojis/:id', 1), ('DELETE /api/comments/:id', 1), ('GET /api/users', 2), ('POST /api/friend-requests', 2), ('POST /api/friend-requests/:id/accept', 2),
 ('POST /api/friend-requests/:id/reject', 1), ('POST /api/friend-requests/:id/cancel', 1), ('DELETE /api/friendships/:id', 1), ('GET /api/relationships/friends', 1), ('GET /api/relationships/following', 1), ('GET /api/relationships/followers', 1),
 ('PUT /api/follows/:id', 1), ('DELETE /api/follows/:id', 1), ('PUT /api/blocks/:id', 1), ('DELETE /api/blocks/:id', 1), ('PATCH /api/me', 1), ('PUT /api/me/profile-image', 3), ('DELETE /api/me/profile-image', 3), ('PATCH /api/posts/:id', 1),
 ('POST /api/me/account-deletion', 5), ('GET /api/memories', 3), ('GET /api/memories/:date', 2), ('GET /api/me/export', 20), ('GET /api/images/:id', 2),
]
pts = dict(rules)
tot = collections.Counter(); cnt = collections.Counter(); byhost = collections.Counter()
for f in glob.glob(d + '/*.ltsv'):
    h = f.split('/')[-1].split('.')[0]
    for line in open(f, errors='replace'):
        r = dict(p.split(':', 1) for p in line.rstrip('\n').split('\t') if ':' in p)
        st = r.get('status', '')
        if not st.startswith('2'):
            continue
        u = UU.sub(':id', r['uri'].split('?')[0])
        u = re.sub(r'^/api/images/.*', '/api/images/:id', u)
        u = re.sub(r'^/api/memories/\d.*', '/api/memories/:date', u)
        k = r['method'] + ' ' + u
        if k in pts:
            tot[k] += pts[k] * mult[h]; cnt[k] += 1; byhost[h] += pts[k] * mult[h]
print('sum', int(sum(tot.values())), {k: int(v) for k, v in byhost.items()})
for k, v in tot.most_common(40):
    print(f'{int(v):8d} {cnt[k]:7d} {k}')
