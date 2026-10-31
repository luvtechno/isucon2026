#!/bin/bash
# ベンチを実行して、結果・ログ・プロファイルをまとめる。usage: runbench.sh <tag> [--no-prof]
tag=${1:-$(date +%H%M%S)}
cd "$(dirname "$0")/.."
scripts/getlogs.sh pre >/dev/null 2>&1
mkdir -p tmp/prof/$tag
node scripts/bench.mjs > tmp/bench-$tag.txt 2>&1 &
BP=$!
if [ "$2" != "--no-prof" ]; then
  scripts/probe-router.sh 90 tmp/prof/$tag/probe.txt &
  sleep 45
  for h in asia1 europe1 america1; do
    ( ssh -F .ssh/config $h 'ss -tin state established "( sport = :80 )" | head -400; nstat -az | grep -E "TcpRetransSegs|TcpExtTCPTimeouts|TcpExtTCPLostRetransmit"; iostat -x 2 5 | grep nvme | cut -c1-110' > tmp/prof/$tag/$h.ss ) &
  done
  for h in asia2 asia1 europe1 america1; do
    ( ssh -F .ssh/config $h 'curl -s -m 40 "http://127.0.0.1:8092/debug/pprof/profile?seconds=30"' > tmp/prof/$tag/$h.pprof ) &
    ( ssh -F .ssh/config $h 'top -bn1 -d 20 | sed -n 3p; sleep 15; top -bn1 | sed -n 1,12p | cut -c1-90; ss -tn state established | wc -l; ss -tn state time-wait | wc -l' > tmp/prof/$tag/$h.top ) &
  done
fi
wait $BP
J=$(grep -oE 'benchmark_jobs/[0-9]+' tmp/bench-$tag.txt | tail -1)
tools/browser.mjs text https://portal.isucon.net/contestant/$J 2>&1 | sed -n '/^Score:/,/^Re-Enqueue/p' | cut -c1-400 > tmp/result-$tag.txt
grep -vE 'Re-Enqueue|^$|Score Breakdown|Conclusion|user moved' tmp/result-$tag.txt | cut -c1-200
scripts/getlogs.sh $tag >/dev/null 2>&1
for h in asia2 asia1 europe1 america1; do ssh -F .ssh/config $h 'journalctl -u isureal-go --since "4 min ago" --no-pager -q | grep -vE "caught up|connected|jpeg check|recovered log|session ended" | cut -c1-200 | tail -4' ; done
