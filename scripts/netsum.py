#!/usr/bin/env python3
"""netmon の結果を5秒ごとに集計する。usage: netsum.py <tag>"""
import sys
tag = sys.argv[1]
hosts = ['asia1', 'europe1', 'america1', 'asia2']
series = {}
for h in hosts:
    rows = [list(map(int, l.split())) for l in open(f'tmp/net/{tag}/{h}.txt') if len(l.split()) >= 6]
    series[h] = rows
print('t    ' + ''.join(f'| {h:8} tx rx MB/s  outEx rtx ' for h in hosts))
n = min(len(v) for v in series.values())
for i in range(5, n, 5):
    line = f'{i:4d} '
    for h in hosts:
        a, b = series[h][i - 5], series[h][i]
        dt = max(1, b[0] - a[0])
        line += f'| {(b[1]-a[1])/dt/1e6:7.0f} {(b[2]-a[2])/dt/1e6:5.0f} {b[3]-a[3]:7d} {b[5]-a[5]:6d} '
    print(line)
