#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
exec bash scripts/with-limits.sh bash -c '
  go vet -p=1 ./... && CGO_ENABLED=1 go test -race -p=1 -parallel=1 -count=1 ./...
'
