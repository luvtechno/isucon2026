#!/bin/bash
# asia1 へ来た接続の一部を asia2 のアプリへ振り分ける。usage: lb-on.sh <確率>
cd "$(dirname "$0")/.."
p=${1:-0.5}
ssh -F .ssh/config asia1 "sudo sysctl -q -w net.ipv4.ip_forward=1; sudo iptables -t nat -F; sudo iptables -t nat -A PREROUTING -p tcp --dport 80 -m statistic --mode random --probability $p -j DNAT --to-destination 192.168.4.10:8080; sudo iptables -t nat -A POSTROUTING -d 192.168.4.10 -p tcp --dport 8080 -j MASQUERADE; sudo iptables -t nat -L -n | head -12; sudo sysctl -q -w net.netfilter.nf_conntrack_max=1000000; sudo sysctl net.netfilter.nf_conntrack_max"
