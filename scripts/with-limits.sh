#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" != --inside ]]; then
  manager=()
  if [[ $(id -u) != 0 ]]; then manager=(--user); fi
  exec systemd-run "${manager[@]}" --scope --quiet --expand-environment=no \
    --property=MemoryMax=4G --property=MemorySwapMax=0 \
    --property=CPUQuota=100% --property=TasksMax=64 \
    bash "${BASH_SOURCE[0]}" --inside "$@"
fi
shift
group=$(awk -F: '$1 == "0" {print $3}' /proc/self/cgroup)
read -r maximum < "/sys/fs/cgroup${group}/memory.max"
if [[ ! $maximum =~ ^[0-9]+$ ]] || (( maximum < 1 || maximum > 4294967296 )); then
  echo 'Нужен проверенный жёсткий лимит памяти cgroup не выше 4 ГиБ.' >&2
  exit 1
fi
echo "Проверено memory.max=$maximum; один процесс сборки." >&2
export GOMAXPROCS=1 GOMEMLIMIT=2GiB GOFLAGS=-p=1 GOTOOLCHAIN=local
exec "$@"
