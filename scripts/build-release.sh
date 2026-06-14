#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$ROOT_DIR"
mkdir -p dist

build() {
  local goos="$1"
  local goarch="$2"
  local suffix="$3"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -o "dist/codex-bridge-$suffix" ./cmd/codex-bridge
}

build linux amd64 linux-amd64
build linux arm64 linux-arm64
build darwin amd64 darwin-amd64
build darwin arm64 darwin-arm64
build windows amd64 windows-amd64.exe
build windows arm64 windows-arm64.exe
