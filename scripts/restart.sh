#!/bin/bash
cd "$(dirname "$0")/.."
for h in asia2 asia1 europe1 america1; do
  ssh -F .ssh/config $h 'sudo systemctl restart isureal-go'
done
