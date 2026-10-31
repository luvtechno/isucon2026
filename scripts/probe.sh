#!/bin/bash
# 期限のずらし幅を試す。usage: probe.sh <秒>
cd "$(dirname "$0")/.."
scripts/setenv.sh ISUREAL_EXP_OFFSET_SEC $1
scripts/restart.sh
sleep 2
node scripts/quick.mjs 25 2>&1 | tail -1 > tmp/q.txt
cut -c1-120 tmp/q.txt
J=$(grep -oE 'benchmark_jobs/[0-9]+' tmp/q.txt | tail -1)
tools/browser.mjs text https://portal.isucon.net/contestant/$J 2>&1 | grep -E '^Reason|^ +"(初期データの検証に失敗|[^"]*範囲外)' | cut -c1-200 | head -3
