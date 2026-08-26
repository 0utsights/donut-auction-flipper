#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
build_dns=${SECOND_PC_BUILD_DNS:-9.9.9.9}
user_id=$(id -u)
group_id=$(id -g)
target=${1:-all}

case "$target" in
  all|backend|collector) ;;
  *) echo "usage: $0 [all|backend|collector]" >&2; exit 2 ;;
esac

mkdir -p "$repo_dir/.second-pc-build" "$repo_dir/collector/.second-pc-build" \
  "$repo_dir/.second-pc-cache/go-build" "$repo_dir/.second-pc-cache/go-mod" "$repo_dir/.second-pc-cache/npm"

services=
if [ "$target" = all ] || [ "$target" = backend ]; then
  docker run --rm --dns "$build_dns" \
    --user "$user_id:$group_id" \
    -e GOCACHE=/tmp/go-build \
    -e GOMODCACHE=/tmp/go-mod \
    -v "$repo_dir:/src:ro" \
    -v "$repo_dir/.second-pc-build:/out" \
    -v "$repo_dir/.second-pc-cache/go-build:/tmp/go-build" \
    -v "$repo_dir/.second-pc-cache/go-mod:/tmp/go-mod" \
    -w /src \
    golang:1.26-alpine \
    sh -c 'CGO_ENABLED=0 go test ./cmd/... ./internal/... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/donut-server ./cmd/server'
  services="$services auction-server"
fi

if [ "$target" = all ] || [ "$target" = collector ]; then
  docker run --rm --dns "$build_dns" \
    --user "$user_id:$group_id" \
    -e HOME=/tmp \
    -e npm_config_cache=/tmp/npm-cache \
    -v "$repo_dir/collector:/src:ro" \
    -v "$repo_dir/collector/.second-pc-build:/out" \
    -v "$repo_dir/.second-pc-cache/npm:/tmp/npm-cache" \
    node:22-alpine \
    sh -c 'mkdir /tmp/collector && cp -R /src/package.json /src/package-lock.json /src/tsconfig.json /src/src /tmp/collector/ && cd /tmp/collector && npm ci && npm test && npm prune --omit=dev && rm -rf /out/node_modules /out/dist && cp package.json package-lock.json /out/ && cp -R node_modules dist /out/'
  services="$services order-collector"
fi

cd "$repo_dir"
# shellcheck disable=SC2086 # services is a deliberate whitespace-separated argv list.
docker compose -f compose.yaml -f compose.second-pc.yaml build $services
