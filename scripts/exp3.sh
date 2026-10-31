#!/bin/bash
# 実験: usage: exp3.sh <tag> <asia softcap> <asia delay spec> <other delay spec> [other softcap] [idle秒]
cd "$(dirname "$0")/.."
tag=$1; X=$2; A=$3; E=$4; O=${5:-0}; IDLE=${6:-150}
# 追加の環境変数は EXTRA に "NAME=VALUE NAME=VALUE" で渡す
sleep $IDLE
for h in asia2 asia1 europe1 america1; do
  cap=$O; spec="$E"; { [ $h = asia1 ] || [ $h = asia2 ]; } && { cap=$X; spec="$A"; }
  ssh -F .ssh/config $h "sed -i '/^ISUREAL_DELAY_SPEC=/d;/^ISUREAL_DELAY_MIN_RPS=/d;/^ISUREAL_POST_SOFTCAP/d' ~/env.sh; echo 'ISUREAL_POST_SOFTCAP=$cap' >> ~/env.sh; echo 'ISUREAL_POST_SOFTCAP_DELAY_MS=2000' >> ~/env.sh; echo 'ISUREAL_DELAY_SPEC=$spec' >> ~/env.sh; echo 'ISUREAL_DELAY_MIN_RPS=0' >> ~/env.sh; sed -i '/^ISUREAL_PARK_/d;/^ISUREAL_STAGGER_MS=/d' ~/env.sh; for kv in $EXTRA; do echo \$kv >> ~/env.sh; done; sudo systemctl restart isureal-go" &
done
wait
sleep 2
scripts/netmon.sh $tag 100 &
scripts/runbench.sh $tag --no-prof > tmp/exp-$tag.txt 2>&1
wait
S=$(grep -m1 '^Score:' tmp/result-$tag.txt | cut -d' ' -f2)
G=$(grep -oE 'グランプリ: .*人' tmp/result-$tag.txt | head -1 | sed -E 's/グランプリ: //; s/アジア /a/; s/ヨーロッパ /e/; s/アメリカ /u/; s/ 人//g')
F=$(grep -E '(asia|europe|america) 5 日目' tmp/result-$tag.txt | grep -oE '[a-z]+ 5 日目: [0-9]+' | sed 's/ 5 日目: /=/' | tr '\n' ' ')
V=$(python3 scripts/viewers.py tmp/logs/$tag | awk '{for(i=1;i<=NF;i++) if($i=="memsum") printf "%s:%s ", $1, $(i+1)}' | sed -E 's/asia1:/a/; s/asia2:0 //; s/europe1:/e/; s/america1:/u/')
echo "$tag cap=$X/$O asia[$A] other[$E] [$EXTRA] score=$S | $F| $G | mem $V" | tee -a tmp/exp-summary.txt
