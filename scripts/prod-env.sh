#!/bin/bash
# 本番の設定を全ノードへ書く。usage: prod-env.sh [log]（log を付けるとアクセスログを残す）
cd "$(dirname "$0")/.."
LOG=${1:-}
for h in asia2 asia1 europe1 america1; do
  cap=0; { [ $h = asia1 ] || [ $h = asia2 ]; } && cap=54
  addr=:80; [ $h = asia2 ] && addr=:8080
  ssh -F .ssh/config $h "sed -i '/^ISUREAL_DELAY_SPEC=/d;/^ISUREAL_DELAY_MIN_RPS=/d;/^ISUREAL_POST_SOFTCAP/d;/^ISUREAL_PARK_/d;/^ISUREAL_STAGGER_MS=/d;/^ISUREAL_HISTIMAGE_SHED/d;/^ISUREAL_MEM_PAGE/d;/^ISUREAL_CLOSED_LOOP/d;/^ISUREAL_IMAGE_CC=/d;/^ISUREAL_ACCESS_LOG=/d;/^ISUREAL_JPEG_DEBUG=/d;/^ISUREAL_EXP_OFFSET_SEC=/d;/^ISUREAL_EXPORT_SHED_RPS=/d;/^ISUREAL_HTTP_ADDR=/d' ~/env.sh; printf 'ISUREAL_HTTP_ADDR=$addr\nISUREAL_POST_SOFTCAP=$cap\nISUREAL_POST_SOFTCAP_DELAY_MS=2000\nISUREAL_STAGGER_MS=2500\nISUREAL_CLOSED_LOOP_TARGET=6500\nISUREAL_CLOSED_LOOP_WAIT_MS=300\n' >> ~/env.sh; [ '$LOG' = log ] && echo ISUREAL_ACCESS_LOG=/home/isucon/isureal-data/access.ltsv >> ~/env.sh; true" &
done
wait
