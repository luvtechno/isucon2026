#!/bin/bash
# 各ホストで毎秒の送受信量と帯域超過カウンタを記録する。usage: netmon.sh <tag> <秒>
cd "$(dirname "$0")/.."
mkdir -p tmp/net/$1
for h in asia1 europe1 america1 asia2; do
  ( ssh -F .ssh/config $h "IF=\$(ip -o -4 route show to default | head -1 | awk '{print \$5}'); for i in \$(seq 1 $2); do echo \"\$(date +%s) \$(cat /sys/class/net/\$IF/statistics/tx_bytes) \$(cat /sys/class/net/\$IF/statistics/rx_bytes) \$(ethtool -S \$IF | grep bw_out | awk '{print \$2}') \$(ethtool -S \$IF | grep bw_in | awk '{print \$2}') \$(awk '/^Tcp:/ && \$1==\"Tcp:\" {c++; if(c==2) print \$13}' /proc/net/snmp)\"; sleep 1; done" > tmp/net/$1/$h.txt ) &
done
wait
