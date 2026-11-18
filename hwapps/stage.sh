#!/usr/bin/env bash
# Copy the bundled hardware apps into <out>/<id>/ with their icons
# (ui/public/icons/off-<id>-{light,dark}.webp). Usage: hwapps/stage.sh <out>
set -euo pipefail
OUT="${1:?usage: hwapps/stage.sh <out>}"
mkdir -p "$OUT" && OUT="$(cd "$OUT" && pwd)"
cd "$(dirname "$0")"
rm -rf "$OUT" && mkdir -p "$OUT"
for d in */; do
	id="${d%/}"
	[ -f "$id/manifest.webmanifest" ] || continue
	mkdir -p "$OUT/$id"
	cp -R "$id/." "$OUT/$id/"
	cp "../ui/public/icons/off-$id-light.webp" "$OUT/$id/icon-light.webp"
	cp "../ui/public/icons/off-$id-dark.webp" "$OUT/$id/icon-dark.webp"
done
