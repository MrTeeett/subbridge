#!/usr/bin/env bash
# Run only in a disposable container: real installation paths, simulated init.
set -euo pipefail
[[ ${SUBBRIDGE_INSTALLER_CONTAINER_TEST:-} == 1 && -f /.dockerenv ]] || {
  echo 'This check requires a disposable Docker container with SUBBRIDGE_INSTALLER_CONTAINER_TEST=1.' >&2
  exit 1
}
[[ $(cat /sys/fs/cgroup/memory.max) == 4294967296 ]] || exit 1
fixture=$(mktemp -d)
export SUBBRIDGE_INSTALL_FIXTURE="$fixture"
test_init=${1:-systemd}
case "$test_init" in
  systemd) mkdir -p /run/systemd/system /etc/systemd/system; unit=/etc/systemd/system/subbridge.service;;
  openrc) mkdir -p /etc/init.d; unit=/etc/init.d/subbridge;;
  *) exit 2;;
esac
export SUBBRIDGE_TEST_UNIT="$unit"
mkdir -p "$fixture/bin"
cat > "$fixture/bin/systemctl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$1" >> "$SUBBRIDGE_INSTALL_FIXTURE/service-calls"
if [[ -f $SUBBRIDGE_INSTALL_FIXTURE/fail ]] && [[ $(cat "$SUBBRIDGE_INSTALL_FIXTURE/fail") == "$1" ]]; then
  rm "$SUBBRIDGE_INSTALL_FIXTURE/fail"
  exit 1
fi
case "$1" in
  is-active) [[ -f $SUBBRIDGE_INSTALL_FIXTURE/active ]];;
  is-enabled) [[ -f $SUBBRIDGE_INSTALL_FIXTURE/enabled ]];;
  start|restart) touch "$SUBBRIDGE_INSTALL_FIXTURE/active";;
  stop) rm -f "$SUBBRIDGE_INSTALL_FIXTURE/active";;
  enable) touch "$SUBBRIDGE_INSTALL_FIXTURE/enabled";;
  disable) rm -f "$SUBBRIDGE_INSTALL_FIXTURE/enabled";;
  daemon-reload) ;;
  *) echo "Unexpected systemctl command: $*" >&2; exit 1;;
esac
EOF
cat > "$fixture/bin/rc-service" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
action=$2
if [[ $action == status ]]; then action=is-active; fi
exec systemctl "$action" subbridge
EOF
cat > "$fixture/bin/rc-update" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
case "$1" in
  add) exec systemctl enable subbridge;;
  del) exec systemctl disable subbridge;;
  *) exit 2;;
esac
EOF
real_install=$(command -v install)
export SUBBRIDGE_TEST_INSTALL_COMMAND="$real_install"
cat > "$fixture/bin/install" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ ${*: -1} == "$SUBBRIDGE_TEST_UNIT" && -f $SUBBRIDGE_INSTALL_FIXTURE/fail ]] && [[ $(cat "$SUBBRIDGE_INSTALL_FIXTURE/fail") == unit-copy ]]; then
  rm "$SUBBRIDGE_INSTALL_FIXTURE/fail"
  exit 1
fi
exec "$SUBBRIDGE_TEST_INSTALL_COMMAND" "$@"
EOF
chmod 755 "$fixture/bin/"*
export PATH="$fixture/bin:$PATH"
version=${SUBBRIDGE_VERSION:-1.1.0}
archive="$PWD/dist/releases/subbridge-${SUBBRIDGE_RELEASE_TAG:-v$version}-linux-amd64.tar.gz"
args=(--archive "$archive" --checksums "$PWD/dist/releases/checksums.txt")
app=/usr/local/lib/subbridge
manager=/usr/local/bin/subbridge
config=/etc/subbridge/config.json
management=/etc/subbridge/management.json
credentials=/etc/subbridge/setup-credentials.txt
lock=/usr/local/lib/.subbridge-install.lock
snapshot() {
  sha256sum "$app/subbridge-core" "$app/install.sh" "$app/subbridge.sh" "$app/repository" "$app/release-channel" "$unit" "$manager" "$config" "$management" "$credentials"
}
# A failed first installation must leave retryable settings, not a broken service.
echo start > "$fixture/fail"
if bash install.sh "${args[@]}"; then echo 'Failed first start was accepted.' >&2; exit 1; fi
[[ ! -e $app && ! -e $manager && ! -e $unit && ! -e $lock ]]
[[ ! -f $fixture/enabled && -f $config && -f $management && -f $credentials ]]
initial_settings=$(sha256sum "$config" "$management" "$credentials")
bash install.sh "${args[@]}" --no-autostart
[[ -f $fixture/active && ! -f $fixture/enabled ]]
[[ $(sha256sum "$config" "$management" "$credentials") == "$initial_settings" ]]
# The real executable must be usable by the actual service account.
su -s /bin/sh -c '/usr/local/lib/subbridge/subbridge-core -config /etc/subbridge/config.json -check' subbridge
printf '\n# previous service fixture\n' >> "$unit"
printf '\n# previous manager fixture\n' >> "$manager"
before=$(snapshot)
# Each failure happens after stopping the old service or switching the app directory.
failure_points=(unit-copy start)
if [[ $test_init == systemd ]]; then failure_points+=(daemon-reload); fi
for point in "${failure_points[@]}"; do
  echo "$point" > "$fixture/fail"
  if bash install.sh "${args[@]}"; then echo "Failure at $point was accepted." >&2; exit 1; fi
  [[ $(snapshot) == "$before" ]]
  [[ -f $fixture/active && ! -f $fixture/enabled && ! -e $lock ]]
done
# Failure to acquire a lock cannot remove someone else's lock or stop the service.
mkdir "$lock"
if bash install.sh "${args[@]}"; then echo 'Concurrent installer was accepted.' >&2; exit 1; fi
[[ -d $lock && $(snapshot) == "$before" && -f $fixture/active ]]
rmdir "$lock"
systemctl stop subbridge
bash install.sh "${args[@]}"
[[ ! -f $fixture/active && ! -f $fixture/enabled && ! -e $lock ]]
[[ $(sha256sum "$config" "$management" "$credentials") == "$initial_settings" ]]
echo "$test_init: service-account permissions, failed first start, update rollback, installer lock, stopped state and autostart preservation passed."
