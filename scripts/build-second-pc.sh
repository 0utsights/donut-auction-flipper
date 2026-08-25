#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
build_dns=${SECOND_PC_BUILD_DNS:-9.9.9.9}
user_id=$(id -u)
group_id=$(id -g)

mkdir -p "$repo_dir/.second-pc-build" "$repo_dir/collector/.second-pc-build"

docker run --rm --dns "$build_dns" \
  --user "$user_id:$group_id" \
  -e GOCACHE=/tmp/go-build \
  -e GOMODCACHE=/tmp/go-mod \
  -v "$repo_dir:/src:ro" \
  -v "$repo_dir/.second-pc-build:/out" \
  -w /src \
  golang:1.26-alpine \
  sh -c 'CGO_ENABLED=0 go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/donut-server ./cmd/server'

docker run --rm --dns "$build_dns" \
  --user "$user_id:$group_id" \
  -e HOME=/tmp \
  -e npm_config_cache=/tmp/npm-cache \
  -v "$repo_dir/collector:/src:ro" \
  -v "$repo_dir/collector/.second-pc-build:/out" \
  node:22-alpine \
  sh -c 'mkdir /tmp/collector && cp -R /src/package.json /src/package-lock.json /src/tsconfig.json /src/src /tmp/collector/ && cd /tmp/collector && npm ci && npm test && npm prune --omit=dev && rm -rf /out/node_modules /out/dist && cp package.json package-lock.json /out/ && cp -R node_modules dist /out/'

cd "$repo_dir"
docker compose -f compose.yaml -f compose.second-pc.yaml build auction-server order-collector
