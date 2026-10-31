#!/bin/bash
# 実験: usage: exp5.sh <tag> <asia softcap> <asia spec> <other spec> <min global rps> [idle]  EXTRA="NAME=VALUE ..."
cd "$(dirname "$0")/.."
tag=$1; X=$2; A=$3; E=$4; R=$5; IDLE=${6:-170}
sleep $IDLE
for h in asia2 asia1 europe1 america1; do
  cap=0; spec="$E"; { [ $h = asia1 ] || [ $h = asia2 ]; } && { cap=$X; spec="$A"; }
  ssh -F .ssh/config $h "sed -i '/^ISUREAL_DELAY_SPEC=/d;/^ISUREAL_DELAY_MIN_RPS=/d;/^ISUREAL_POST_SOFTCAP/d;/^ISUREAL_PARK_/d;/^ISUREAL_STAGGER_MS=/d;/^ISUREAL_HISTIMAGE_SHED_RPS=/d;/^ISUREAL_HISTIMAGE_SHED_PCT=/d;/^ISUREAL_CLOSED_LOOP/d;/^ISUREAL_IMAGE_CC=/d' ~/env.sh; echo 'ISUREAL_POST_SOFTCAP=$cap' >> ~/env.sh; echo 'ISUREAL_POST_SOFTCAP_DELAY_MS=2000' >> ~/env.sh; echo 'ISUREAL_DELAY_SPEC=$spec' >> ~/env.sh; echo 'ISUREAL_DELAY_MIN_RPS=$R' >> ~/env.sh; for kv in $EXTRA; do echo \$kv >> ~/env.sh; done; sudo systemctl restart isureal-go" &
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
echo "$tag cap=$X asia[$A] other[$E]@$R [$EXTRA] score=$S | $F| $G | mem $V" | tee -a tmp/exp-summary.txt
python3 scripts/total.py tmp/logs/$tag | cut -c1-210
grep -E "RUNNING|FINISHED" tmp/bench-$tag.txt | awk '{print $4}' | head -16 | tr '\n' ' '; echo
