#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"
version=${SUBBRIDGE_VERSION:-1.1.0}
exec bash scripts/with-limits.sh env CGO_ENABLED=0 go run -p=1 ./tools/package -version "$version" -tag "${SUBBRIDGE_RELEASE_TAG:-v$version}"
