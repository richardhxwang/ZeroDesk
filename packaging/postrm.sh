#!/bin/sh
set -e
# postinst may have written this drop-in (device account not called 'pi');
# remove it with the package. Same variable form as in postinst.sh.
dropin_dir=/etc/systemd/system/zerodesk-cp0.service.d
rm -f "$dropin_dir/device-user.conf" 2>/dev/null || true
rmdir "$dropin_dir" 2>/dev/null || true
# and this tmpfiles override, plus the directory it made
tmpfiles_dir=/etc/tmpfiles.d
rm -f "$tmpfiles_dir/zerodesk-cp0.conf" 2>/dev/null || true
run_dir=/run/zerodesk-cp0
rm -rf "$run_dir" 2>/dev/null || true
if [ -d /run/systemd/system ]; then
	systemctl daemon-reload >/dev/null 2>&1 || true
fi
exit 0
