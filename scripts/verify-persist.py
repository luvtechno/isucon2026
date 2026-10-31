#!/usr/bin/env python3
"""再起動をまたいでデータが残ることを確かめる。usage: verify-persist.py write|check <state.json>"""
import json, sys, time, urllib.request, urllib.error, uuid, io, base64
R = 'http://ROUTER_HOST:8080'
def req(method, path, token=None, body=None, cc='JP', ctype='application/json', raw=None):
    data = raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
    r = urllib.request.Request(R + path, data=data, method=method)
    r.add_header('X-Country-Code', cc)
    if token: r.add_header('Authorization', 'Bearer ' + token)
    if data is not None: r.add_header('Content-Type', ctype)
    try:
        with urllib.request.urlopen(r, timeout=30) as f:
            return f.status, f.read()
    except urllib.error.HTTPError as e:
        return e.code, e.read()
JPEG = base64.b64decode('/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAAMCAgICAgMCAgIDAwMDBAYEBAQEBAgGBgUGCQgKCgkICQkKDA8MCgsOCwkJDRENDg8QEBEQCgwSExIQEw8QEBD/wAALCAABAAEBAREA/8QAFAABAAAAAAAAAAAAAAAAAAAACv/EABQQAQAAAAAAAAAAAAAAAAAAAAD/2gAIAQEAAD8AVN//2Q==')
def multipart(fields, fname, data):
    b = uuid.uuid4().hex
    out = io.BytesIO()
    for k, v in fields.items():
        out.write(f'--{b}\r\nContent-Disposition: form-data; name="{k}"\r\n\r\n{v}\r\n'.encode())
    out.write(f'--{b}\r\nContent-Disposition: form-data; name="image"; filename="{fname}"\r\nContent-Type: image/jpeg\r\n\r\n'.encode())
    out.write(data); out.write(f'\r\n--{b}--\r\n'.encode())
    return out.getvalue(), f'multipart/form-data; boundary={b}'
mode, path = sys.argv[1], sys.argv[2]
if mode == 'write':
    st = {}
    print('init', req('POST', '/api/initialize')[0])
    code, b = req('POST', '/api/admin/auth/login', body={'username': 'admin', 'password': 'password1234'})
    admin = json.loads(b)['token']
    print('window', req('POST', '/api/admin/posting-window', token=admin, body={'region': 'europe', 'localDate': '2026-11-20'})[0])
    print('window', req('POST', '/api/admin/posting-window', token=admin, body={'region': 'asia', 'localDate': '2026-11-20'})[0])
    name = 'persist' + uuid.uuid4().hex[:8]
    code, b = req('POST', '/api/auth/register', body={'username': name, 'displayName': '再起動テスト', 'region': 'europe'}, cc='GB')
    d = json.loads(b); st['user'] = name; st['secret'] = d['secretKey']; st['uid'] = d['user']['id']; print('register', code)
    code, b = req('POST', '/api/auth/login', body={'username': name, 'secretKey': st['secret']}, cc='GB')
    tok = json.loads(b)['token']; st['token'] = tok
    print('patch', req('PATCH', '/api/me', token=tok, body={'bio': '再起動のあとも残る'}, cc='GB')[0])
    print('profile', req('PUT', '/api/me/profile-image', token=tok, raw=JPEG, ctype='image/jpeg', cc='GB')[0])
    raw, ct = multipart({'caption': '再起動の前の投稿', 'visibility': 'public', 'location_kind': 'none'}, 'a.jpg', JPEG)
    code, b = req('POST', '/api/posts', token=tok, raw=raw, ctype=ct, cc='GB'); print('post', code)
    p = json.loads(b); st['post'] = p['id']; st['image'] = p['imageUrl']
    print('comment', req('POST', f"/api/posts/{st['post']}/comments", token=tok, body={'body': 'こんにちは'}, cc='GB')[0])
    json.dump(st, open(path, 'w'))
    print(json.dumps(st, ensure_ascii=False)[:300])
else:
    st = json.load(open(path))
    code, b = req('POST', '/api/auth/login', body={'username': st['user'], 'secretKey': st['secret']}, cc='GB')
    print('login', code)
    tok = json.loads(b)['token']
    code, b = req('GET', '/api/me', token=tok, cc='GB'); me = json.loads(b); print('me', code, me['bio'], me['profileImageUrl'] is not None)
    code, b = req('GET', f"/api/posts/{st['post']}", token=tok, cc='GB'); p = json.loads(b); print('post', code, p['caption'], p['imageUrl'][:60])
    code, b = req('GET', p['imageUrl'], cc='GB'); print('image', code, len(b))
    code, b = req('GET', me['profileImageUrl'], cc='GB'); print('profile-image', code, len(b))
    code, b = req('GET', f"/api/posts/{st['post']}/comments", token=tok, cc='GB'); print('comments', code, [c['body'] for c in json.loads(b)['items']])
    code, b = req('GET', '/api/memories?month=2026-11', token=tok, cc='GB'); print('memories', code, len(json.loads(b)['items']))
    code, b = req('GET', '/api/me/export', token=tok, cc='GB'); print('export', code, len(b))
    code, b = req('GET', '/api/posting-window/current', token=tok, cc='GB'); print('window', code, b[:120])
