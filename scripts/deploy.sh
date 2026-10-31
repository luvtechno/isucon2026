#!/bin/bash
# Go のアプリをビルドして4台へ入れ、再起動する。usage: deploy.sh [host...]
set -e
cd "$(dirname "$0")/.."
hosts=${@:-asia2 asia1 europe1 america1}
(cd webapp/go && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/isureal-linux ./cmd/isureal)
for h in $hosts; do
  rsync -az -e "ssh -F .ssh/config" /tmp/isureal-linux $h:/tmp/isureal-new &
done
wait
for h in $hosts; do
  ssh -F .ssh/config $h 'sudo systemctl stop isureal-go; mv /tmp/isureal-new ~/webapp/go/isureal; sudo systemctl start isureal-go; sleep 0.3; echo "$(hostname) $(systemctl is-active isureal-go)"'
done
