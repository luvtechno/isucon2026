#!/bin/bash
# 各ホストの access.ltsv を tmp/logs/<tag>/ へ取って、次の走行のために空にする
tag=${1:-$(date +%H%M%S)}
cd "$(dirname "$0")/.."
mkdir -p tmp/logs/$tag
for h in asia1 asia2 europe1 america1; do
  ( ssh -F .ssh/config $h "cat ~/isureal-data/access.ltsv 2>/dev/null; truncate -s0 ~/isureal-data/access.ltsv 2>/dev/null; true" > tmp/logs/$tag/$h.ltsv ) &
done
wait
ls -la tmp/logs/$tag
