#!/bin/bash
# ネットワークの調整を全ノードへ当てる。usage: net-tune.sh <rto_min ms|0> <per-flow maxrate(例 1gbit)|off>
cd "$(dirname "$0")/.."
RTO=${1:-0}; RATE=${2:-off}
for h in asia1 europe1 america1 asia2; do
  ssh -F .ssh/config $h "IF=\$(ip -o -4 route show to default | head -1 | awk '{print \$5}'); GW=\$(ip -o -4 route show to default | head -1 | awk '{print \$3}');
    if [ '$RTO' != 0 ]; then sudo ip route change default via \$GW dev \$IF proto dhcp src \$(ip -o -4 addr show \$IF | awk '{print \$4}' | cut -d/ -f1) metric 100 rto_min ${RTO}ms; else sudo ip route change default via \$GW dev \$IF proto dhcp src \$(ip -o -4 addr show \$IF | awk '{print \$4}' | cut -d/ -f1) metric 100; fi;
    if [ '$RATE' != off ]; then sudo modprobe sch_fq; for q in 1 2; do sudo tc qdisc replace dev \$IF parent :\$q fq maxrate $RATE; done; else for q in 1 2; do sudo tc qdisc replace dev \$IF parent :\$q fq_codel; done; fi;
    ip route show default | cut -c1-110; tc qdisc show dev \$IF | head -2 | cut -c1-110" &
done
wait
