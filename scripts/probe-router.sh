#!/bin/bash
# 走行中にルーター越しの遅延を手元から測る。usage: probe-router.sh <秒> <out>
end=$(( $(date +%s) + $1 ))
while [ $(date +%s) -lt $end ]; do
  for cc in JP GB US; do
    curl -s -o /dev/null -m 5 -w "$(date +%H:%M:%S) $cc %{http_code} conn=%{time_connect} total=%{time_total}\n" -H "X-Country-Code: $cc" http://ROUTER_HOST:8080/index.html
  done
  sleep 1
done > "$2"
