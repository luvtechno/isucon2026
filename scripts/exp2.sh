#!/bin/bash
# 実験: リージョンごとの投稿のソフト上限を変えてベンチを走らせ、結果を1行で残す。
# usage: exp2.sh <tag> <asia の上限> <delay ms> [europe/america の上限]
cd "$(dirname "$0")/.."
tag=$1; X=$2; DMS=${3:-2000}; O=${4:-0}
for h in asia2 asia1 europe1 america1; do
  cap=$O; { [ $h = asia1 ] || [ $h = asia2 ]; } && cap=$X
  ssh -F .ssh/config $h "sed -i '/^ISUREAL_DELAY_SPEC=/d;/^ISUREAL_DELAY_MIN_RPS=/d;/^ISUREAL_POST_SOFTCAP/d' ~/env.sh; echo 'ISUREAL_POST_SOFTCAP=$cap' >> ~/env.sh; echo 'ISUREAL_POST_SOFTCAP_DELAY_MS=$DMS' >> ~/env.sh; sudo systemctl restart isureal-go" &
done
wait
sleep 2
scripts/netmon.sh $tag 110 &
scripts/runbench.sh $tag --no-prof > tmp/exp-$tag.txt 2>&1
wait
S=$(grep -m1 '^Score:' tmp/result-$tag.txt | cut -d' ' -f2)
G=$(grep -oE 'グランプリ: .*人' tmp/result-$tag.txt | head -1)
D=$(grep -E '(asia|europe|america) [0-9] 日目' tmp/result-$tag.txt | grep -oE '[a-z]+ [0-9] 日目: [0-9]+' | sed -E 's/ ([0-9]) 日目: /\1=/' | grep asia | sed 's/asia//' | tr '\n' ' ')
F=$(grep -E '(asia|europe|america) 5 日目' tmp/result-$tag.txt | grep -oE '[a-z]+ 5 日目: [0-9]+' | sed 's/ 5 日目: /=/' | tr '\n' ' ')
M=$(grep -c '^    "' tmp/result-$tag.txt)
echo "$tag softcap=$X delay=$DMS other=$O score=$S | asia: $D| $F| $G | msgs=$M" | tee -a tmp/exp-summary.txt
