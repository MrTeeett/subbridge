#!/usr/bin/env bash
set -euo pipefail
group=$(awk -F: '$1=="0" {print $3}' /proc/self/cgroup)
read -r maximum < "/sys/fs/cgroup$group/memory.max"
[[ $maximum =~ ^[0-9]+$ ]] && ((maximum>0 && maximum<=4294967296)) || { echo 'A verified cgroup limit <=4 GiB is required.' >&2; exit 1; }
case "$(go env GOVERSION)" in go1.26.*) ;; *) echo 'Go 1.26.x is required.' >&2; exit 1;; esac
export GOMAXPROCS=1 GOMEMLIMIT=2GiB GOFLAGS=-p=1 GOTOOLCHAIN=local
target=${1:?}; release=${2:?}
[[ $release =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][a-zA-Z0-9.-]+)?$ ]] || { echo 'Invalid version.' >&2; exit 1; }
targets=("$target")
if [[ $target == all ]]; then targets=(linux-amd64 linux-arm64 linux-armv7 linux-386 windows-amd64 windows-arm64); fi
mkdir -p dist
for target in "${targets[@]}"; do
  target_os=${target%%-*}; architecture=${target#*-}
  if [[ $architecture == armv7 ]]; then architecture=arm; fi
  suffix=; if [[ $target_os == windows ]]; then suffix=.exe; fi
  GOOS="$target_os" GOARCH="$architecture" GOARM=7 CGO_ENABLED=0 go build -p=1 -trimpath \
    -ldflags="-s -w -X main.version=$release" -o "dist/subbridge-$target$suffix" .
done
if [[ -f dist/subbridge-linux-amd64 ]]; then
  cp dist/subbridge-linux-amd64 dist/subbridge.new
  mv -f dist/subbridge.new dist/subbridge
fi
