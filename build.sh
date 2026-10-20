#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
target=${1:-linux-amd64}
case "$target" in amd64|arm64) target="linux-$target";; esac
case "$target" in all|linux-amd64|linux-arm64|linux-armv7|linux-386|windows-amd64|windows-arm64) ;;
  *) echo 'Target: all, linux-{amd64,arm64,armv7,386}, windows-{amd64,arm64}' >&2; exit 1;; esac
exec bash scripts/with-limits.sh bash scripts/build-inside.sh "$target" "${SUBBRIDGE_VERSION:-1.1.0}"
