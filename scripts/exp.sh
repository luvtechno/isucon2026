#!/bin/bash
# 実験: 遅延の設定を変えてベンチを走らせ、結果を1行で残す。
# usage: exp.sh <tag> "<asia の spec>" "<europe/america の spec>" [min_rps]
cd "$(dirname "$0")/.."
tag=$1; A=$2; E=$3; R=${4:-0}
for h in asia1 europe1 america1; do
  spec="$E"; [ $h = asia1 ] && spec="$A"
  ssh -F .ssh/config $h "sed -i '/^ISUREAL_DELAY_SPEC=/d;/^ISUREAL_DELAY_MIN_RPS=/d' ~/env.sh; echo 'ISUREAL_DELAY_SPEC=$spec' >> ~/env.sh; echo 'ISUREAL_DELAY_MIN_RPS=$R' >> ~/env.sh; sudo systemctl restart isureal-go" &
done
wait
sleep 2
scripts/runbench.sh $tag --no-prof > tmp/exp-$tag.txt 2>&1
S=$(grep -m1 '^Score:' tmp/result-$tag.txt | cut -d' ' -f2)
G=$(grep -oE 'グランプリ: .*人' tmp/result-$tag.txt | head -1)
D=$(grep -E '(asia|europe|america) 5 日目' tmp/result-$tag.txt | grep -oE '[a-z]+ 5 日目: [0-9]+' | sed 's/ 5 日目: /=/' | tr '\n' ' ')
M=$(grep -c '^    "' tmp/result-$tag.txt)
echo "$tag asia[$A] other[$E] rps[$R] score=$S | $D| $G | msgs=$M" | tee -a tmp/exp-summary.txt
