#!/bin/bash
# Go のアプリをビルドして3台へ入れ、再起動する。usage: deploy.sh [host...]
set -e
cd "$(dirname "$0")/.."
hosts=${@:-asia1 europe1 america1}
(cd webapp/go && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/isureal-linux ./cmd/isureal)
for h in $hosts; do
  (
    rsync -az -e "ssh -F .ssh/config" /tmp/isureal-linux $h:/tmp/isureal-new
    rsync -az --delete --exclude '._*' -e "ssh -F .ssh/config" webapp/sql/ $h:webapp/sql/
    ssh -F .ssh/config $h 'sudo systemctl stop isureal-go; mv /tmp/isureal-new ~/webapp/go/isureal; sudo systemctl start isureal-go; sleep 0.5; systemctl is-active isureal-go'
  ) &
done
wait
