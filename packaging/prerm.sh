#!/bin/sh
set -e
if [ "$1" = "remove" ] || [ "$1" = "deconfigure" ]; then
	if [ -d /run/systemd/system ]; then
		systemctl disable --now zerodesk-cp0.path >/dev/null 2>&1 || true
		systemctl disable --now zerodesk-cp0.service >/dev/null 2>&1 || true
	fi
	# web-app backends ZeroDesk started in the user's session
	for u in $(loginctl list-users --no-legend 2>/dev/null | awk '$1 >= 1000 { print $2 }'); do
		systemctl --user -M "$u@" stop 'zerodesk-app-*.service' >/dev/null 2>&1 || true
	done
fi
exit 0
