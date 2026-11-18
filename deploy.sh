#!/usr/bin/env bash
# Build the UI and the arm64 agent, install on the Zero, keep port 80 working.
# Usage: ./deploy.sh <user@device-host>
set -euo pipefail
HOST="${1:?usage: ./deploy.sh <user@device-host>}"
cd "$(dirname "$0")"

(cd ui && pnpm build)
(cd agent && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o build/zerodesk .)
hwapps/stage.sh agent/build/apps

# one compressed stream (about half the size): matters on slow relayed links
COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata -cf - -C agent/build zerodesk -C ../deploy zerodesk.service -C ../../vfb zd_vfb.c -C ../agent/build apps \
	| xz -6 -T0 \
	| ssh -4 -o ConnectTimeout=8 -o ServerAliveInterval=10 -o ServerAliveCountMax=6 "$HOST" \
		'rm -rf /tmp/zd-deploy && mkdir /tmp/zd-deploy && xz -dc | tar -xf - -C /tmp/zd-deploy &&
		 mv /tmp/zd-deploy/zerodesk /tmp/zerodesk.new && mv /tmp/zd-deploy/zerodesk.service /tmp/zerodesk.service &&
		 mv /tmp/zd-deploy/zd_vfb.c /tmp/zd_vfb.c &&
		 rm -rf /tmp/zd-apps && mv /tmp/zd-deploy/apps /tmp/zd-apps && rmdir /tmp/zd-deploy'
ssh -4 -o ConnectTimeout=8 -o ServerAliveInterval=10 -o ServerAliveCountMax=6 "$HOST" '
set -e
mkdir -p ~/.local/bin ~/.config/systemd/user
install -m 755 /tmp/zerodesk.new ~/.local/bin/zerodesk
install -m 644 /tmp/zerodesk.service ~/.config/systemd/user/zerodesk.service
rm -f /tmp/zerodesk.new /tmp/zerodesk.service
# bundled hardware apps (hwapps/): the dev install'"'"'s share dir is the per-user apps dir
mkdir -p ~/.local/share/zerodesk/apps
for d in /tmp/zd-apps/*/; do id=$(basename "$d"); rm -rf ~/.local/share/zerodesk/apps/"$id"; cp -R "$d" ~/.local/share/zerodesk/apps/"$id"; done
rm -rf /tmp/zd-apps
# virtual-screen library for apps opened on the computer
mkdir -p ~/.local/lib/zerodesk
gcc -O2 -shared -fPIC -o ~/.local/lib/zerodesk/zd_vfb.so /tmp/zd_vfb.c -ldl
rm -f /tmp/zd_vfb.c
# replacing the file drops file capabilities, so grant the port-80 bind again
sudo -n /usr/sbin/setcap cap_net_bind_service=+ep ~/.local/bin/zerodesk \
	|| echo "warning: no passwordless sudo, port 80 stays off until: sudo setcap cap_net_bind_service=+ep ~/.local/bin/zerodesk"
systemctl --user daemon-reload
systemctl --user enable zerodesk.service >/dev/null 2>&1
systemctl --user restart zerodesk.service
sleep 1
systemctl --user is-active zerodesk.service
journalctl --user -u zerodesk -n 2 --no-pager -o cat
'
