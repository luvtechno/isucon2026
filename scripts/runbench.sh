#!/bin/bash
# ベンチを実行して、結果とログをまとめる。usage: runbench.sh <tag>
tag=${1:-$(date +%H%M%S)}
cd "$(dirname "$0")/.."
scripts/getlogs.sh pre >/dev/null 2>&1
node scripts/bench.mjs 2>&1 | tail -2 > tmp/bench-$tag.txt
J=$(grep -oE 'benchmark_jobs/[0-9]+' tmp/bench-$tag.txt | tail -1)
tools/browser.mjs text https://portal.isucon.net/contestant/$J 2>&1 | sed -n '/^Score:/,/^Re-Enqueue/p' | cut -c1-400 > tmp/result-$tag.txt
grep -vE 'Re-Enqueue|^$|Score Breakdown|Conclusion' tmp/result-$tag.txt | cut -c1-220
scripts/getlogs.sh $tag >/dev/null 2>&1
for h in asia2 asia1 europe1 america1; do ssh -F .ssh/config $h 'journalctl -u isureal-go --since "4 min ago" --no-pager -q | grep -vE "caught up|connected|jpeg check" | cut -c1-240 | tail -5' ; done
