#!/usr/bin/env bash
# Build ZeroDesk for arm64, run it in a container that stands in for a
# CardputerZero, and run the browser checks against it.
#   tests/e2e/run.sh                 # all checks
#   tests/e2e/run.sh terminal t3     # one file, tests whose name starts with t3
# Env: ZD_E2E_PORT (18700), ZD_E2E_IMAGE / ZD_E2E_CONTAINER (zd-demo), ZD_E2E_SKIP_BUILD=1
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
port=${ZD_E2E_PORT:-18700}
image=${ZD_E2E_IMAGE:-zd-demo}
container=${ZD_E2E_CONTAINER:-zd-demo}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

if [[ -z "${ZD_E2E_SKIP_BUILD:-}" ]]; then
	echo "== build"
	(cd "$root/ui" && npm run build >"$work/ui.log" 2>&1) || { cat "$work/ui.log"; exit 1; }
	(cd "$root/agent" && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o "$work/zerodesk" .)
fi

if ! docker image inspect "$image" >/dev/null 2>&1; then
	[[ -f "$work/zerodesk" ]] || { echo "no image $image: run without ZD_E2E_SKIP_BUILD once" >&2; exit 1; }
	cp "$here/Dockerfile" "$work/"
	docker build --platform linux/arm64 -t "$image" "$work" >/dev/null
fi

if ! docker container inspect "$container" >/dev/null 2>&1; then
	docker run -d --name "$container" --platform linux/arm64 --hostname CardputerZero \
		-p "127.0.0.1:$port:8686" "$image" >/dev/null
fi
[[ -f "$work/zerodesk" ]] && docker cp "$work/zerodesk" "$container:/usr/local/bin/zerodesk"
docker restart "$container" >/dev/null
for _ in $(seq 1 30); do
	curl -s -o /dev/null "http://127.0.0.1:$port/" && break
	sleep 1
done

[[ -d "$here/node_modules" ]] || (cd "$here" && npm ci --silent)

# the proxy in the shell environment must not see localhost traffic
export NO_PROXY='*'
status=0
for f in ${1:-split terminal}; do
	echo "== $f"
	node "$here/$f.mjs" "$port" ${2:-} || status=1
done
exit $status
