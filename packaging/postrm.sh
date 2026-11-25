#!/bin/sh
set -e
rm -f /etc/systemd/system/zerodesk-cp0.service.d/device-user.conf 2>/dev/null || true
rmdir /etc/systemd/system/zerodesk-cp0.service.d 2>/dev/null || true
if [ -d /run/systemd/system ]; then
	systemctl daemon-reload >/dev/null 2>&1 || true
fi
exit 0
