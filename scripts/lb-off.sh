#!/bin/bash
cd "$(dirname "$0")/.."
ssh -F .ssh/config asia1 "sudo iptables -t nat -F; sudo sysctl -q -w net.ipv4.ip_forward=0"
