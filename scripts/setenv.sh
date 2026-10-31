#!/bin/bash
# 全ノードの env.sh の変数を1つ書き換える（値が空なら消す）。usage: setenv.sh NAME VALUE
cd "$(dirname "$0")/.."
for h in asia2 asia1 europe1 america1; do
  ssh -F .ssh/config $h "sed -i '/^$1=/d' ~/env.sh; [ -n '$2' ] && echo '$1=$2' >> ~/env.sh; true" &
done
wait
