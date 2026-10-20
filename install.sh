#!/usr/bin/env bash
# SubBridge installer. Installs release binaries; never compiles on the server.
set -euo pipefail
umask 077
repository=${SUBBRIDGE_REPO:-MrTeeett/subbridge}
release=latest
release_selected=0
archive=
checksums=
stage_root=
enable_boot=1
# Release downloads and validation finish before the live installation is stopped.
temporary=
new_dir=
lock_dir=
lock_owned=0
transaction=0
committed=0
app_moved=0
app_published=0
manager_written=0
unit_written=0
enable_changed=0
was_active=0
was_enabled=0
installed=0
had_manager=0
had_unit=0
backup=
unit=
fail() { printf 'Error: %s\n' "$*" >&2; exit 1; }
service() {
  if [[ $init == systemd ]]; then systemctl "$1" subbridge
  else rc-service subbridge "$1"; fi
}
cleanup() {
  local result=$? rollback_failed=0
  trap - EXIT INT TERM
  set +e
  if ((transaction && !committed)); then
    echo 'Installation failed; restoring the previous installation.' >&2
    if [[ $init != stage ]]; then service stop >/dev/null 2>&1; fi
    if ((enable_changed)); then
      if [[ $init == systemd ]]; then systemctl disable subbridge || rollback_failed=1
      else rc-update del subbridge default || rollback_failed=1; fi
    fi
    if ((app_published)); then rm -rf -- "$app_dir" || rollback_failed=1; fi
    if ((app_moved)); then mv -- "$backup" "$app_dir" || rollback_failed=1; fi
    if ((manager_written)); then
      if ((had_manager)); then install -m 755 "$temporary/previous-manager" "$manager" || rollback_failed=1
      else rm -f -- "$manager" || rollback_failed=1; fi
    fi
    if ((unit_written)); then
      if ((had_unit)); then cp -p -- "$temporary/previous-unit" "$unit" || rollback_failed=1
      else
        if [[ $init == systemd ]]; then systemctl disable subbridge >/dev/null 2>&1
        else rc-update del subbridge default >/dev/null 2>&1; fi
        rm -f -- "$unit" || rollback_failed=1
      fi
      if [[ $init == systemd ]]; then systemctl daemon-reload || rollback_failed=1; fi
    fi
    if ((was_active)); then service start || rollback_failed=1; fi
    if ((rollback_failed)); then
      echo "Automatic rollback needs attention. Check $app_dir and $backup; configuration and data were kept." >&2
    elif ((installed)); then echo 'Previous binary, console manager and service definition restored.' >&2
    else echo 'Incomplete installation removed; configuration and credentials were kept for the next attempt.' >&2; fi
    if ((result == 0)); then result=1; fi
  fi
  if [[ -n $new_dir && -d $new_dir ]]; then rm -rf -- "$new_dir"; fi
  if [[ -n $temporary ]]; then rm -rf -- "$temporary"; fi
  if ((lock_owned)); then rmdir -- "$lock_dir"; fi
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
usage() {
  cat <<'EOF'
SubBridge installer (Linux)
  bash install.sh [-dev | --version v1.1.0] [--repo owner/repo] [--no-autostart]
  bash install.sh --archive /path/subbridge-v1.1.0-linux-amd64.tar.gz --checksums /path/checksums.txt
  -dev, --dev                 Install the rolling dev prerelease instead of latest stable.
  --stage-dir /absolute/path   Extract and initialize only; no users/services/system changes.
EOF
}
require_value() {
  if (($# < 2)) || [[ -z $2 || $2 == -* ]]; then
    printf 'Option %s requires a value. See --help.\n' "$1" >&2
    exit 2
  fi
}
while (($#)); do
  case "$1" in
    --repo) require_value "$@"; repository=${2:?}; shift 2;;
    --version|-dev|--dev)
      ((!release_selected)) || { echo 'Choose either -dev or --version, once.' >&2; exit 2; }
      release_selected=1
      if [[ $1 == --version ]]; then require_value "$@"; release=$2; shift 2
      else release=dev; shift; fi;;
    --archive) require_value "$@"; archive=${2:?}; shift 2;;
    --checksums) require_value "$@"; checksums=${2:?}; shift 2;;
    --stage-dir) require_value "$@"; stage_root=${2:?}; shift 2;;
    --no-autostart) enable_boot=0; shift;;
    -h|--help) usage; exit 0;;
    *) echo "Unknown option: $1" >&2; exit 2;;
  esac
done
[[ $repository =~ ^[a-zA-Z0-9_.-]+/[a-zA-Z0-9_.-]+$ ]] || { echo 'Invalid repository.' >&2; exit 1; }
[[ $release == latest || $release == dev || $release =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][a-zA-Z0-9.-]+)?$ ]] || { echo 'Invalid release tag.' >&2; exit 1; }
if [[ -n $archive || -n $checksums ]]; then
  [[ -n $archive && -n $checksums ]] || fail 'Use --archive and --checksums together.'
  ((!release_selected)) || fail 'Choose a local archive or a release tag, not both.'
  [[ -f $archive && -f $checksums ]] || fail 'Archive or checksums.txt does not exist.'
fi
[[ $(uname -s) == Linux ]] || { echo 'Use install.ps1 on Windows.' >&2; exit 1; }
case "$(uname -m)" in
  x86_64|amd64) architecture=amd64;;
  aarch64|arm64) architecture=arm64;;
  armv7l|armv8l) architecture=armv7;;
  i386|i486|i586|i686) architecture=386;;
  *) echo 'Supported architectures: amd64, arm64, armv7, 386.' >&2; exit 1;;
esac
if [[ -n $stage_root ]]; then
  [[ $stage_root == /* && $stage_root != / && $stage_root != *[[:space:]]* && $stage_root != *'..'* ]] || { echo 'Stage directory must be an absolute path without spaces or ..' >&2; exit 1; }
  mkdir -p -- "$stage_root"
  stage_root=$(cd -- "$stage_root" && pwd -P)
else
  [[ $(id -u) == 0 ]] || { echo 'Run as root (sudo bash install.sh).' >&2; exit 1; }
fi
missing=0
required=(tar gzip sha256sum awk sed install mktemp)
if [[ -z $archive ]]; then required+=(curl); fi
for command in "${required[@]}"; do command -v "$command" >/dev/null || missing=1; done
if ((missing)); then
  [[ -z $stage_root ]] || { echo 'Stage mode is missing required utilities. Install tar, gzip, coreutils, awk, sed, and curl for downloads.' >&2; exit 1; }
  if command -v apt-get >/dev/null; then apt-get update; apt-get install -y curl tar gzip ca-certificates coreutils
  elif command -v dnf >/dev/null; then dnf install -y curl tar gzip ca-certificates coreutils
  elif command -v yum >/dev/null; then yum install -y curl tar gzip ca-certificates coreutils
  elif command -v apk >/dev/null; then apk add --no-cache bash curl tar gzip ca-certificates coreutils
  elif command -v pacman >/dev/null; then pacman -S --needed --noconfirm curl tar gzip ca-certificates coreutils
  elif command -v zypper >/dev/null; then zypper --non-interactive install curl tar gzip ca-certificates coreutils
  else echo 'Install curl, tar, ca-certificates and sha256sum first.' >&2; exit 1; fi
fi
if [[ -n $stage_root ]]; then init=stage
elif [[ -d /run/systemd/system ]] && command -v systemctl >/dev/null; then init=systemd
elif command -v rc-service >/dev/null && command -v rc-update >/dev/null; then init=openrc
else echo 'Supported init systems: systemd and OpenRC.' >&2; exit 1; fi
app_dir="$stage_root/usr/local/lib/subbridge"
config_dir="$stage_root/etc/subbridge"
data_dir="$stage_root/var/lib/subbridge"
manager="$stage_root/usr/local/bin/subbridge"
install -d -m 755 -- "$(dirname -- "$app_dir")" "$(dirname -- "$manager")"
lock_dir="$(dirname -- "$app_dir")/.subbridge-install.lock"
mkdir -m 700 -- "$lock_dir" 2>/dev/null || fail "Another installer is running. If a previous installer was killed, remove the stale lock: $lock_dir"
lock_owned=1
if [[ -d $app_dir ]]; then
  [[ -f $app_dir/subbridge-core && -f $app_dir/subbridge.sh && -f $app_dir/install.sh ]] || fail "Incomplete or unrelated installation at $app_dir. Move it aside before installing."
  installed=1
fi
if [[ -e $manager || -L $manager ]]; then
  ((installed)) || fail 'A command named subbridge already exists outside this installation. Move it first.'
  had_manager=1
fi
temporary=$(mktemp -d)
printf 'SubBridge installer: Linux %s, %s\n' "$architecture" "$init"
fetch() { curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' --tlsv1.2 --connect-timeout 15 --max-time 180 --retry 2 --output "$2" "$1"; }
if [[ -z $archive ]]; then
  if [[ $release == latest ]]; then
    if ! fetch "https://api.github.com/repos/$repository/releases/latest" "$temporary/release.json"; then
      fail "Cannot load the latest stable release from $repository. For a new repository, wait for the GitHub Actions build and run: bash install.sh -dev"
    fi
    release=$(sed -nE 's/.*"tag_name"[[:space:]]*:[[:space:]]*"(v[0-9]+\.[0-9]+\.[0-9]+([.-][a-zA-Z0-9.-]+)?)".*/\1/p' "$temporary/release.json" | head -n 1)
    [[ -n $release ]] || { echo 'No published release was found.' >&2; exit 1; }
  fi
  asset="subbridge-$release-linux-$architecture.tar.gz"
  archive="$temporary/$asset"
  checksums="$temporary/checksums.txt"
  echo "Downloading $release ($architecture) from $repository..."
  fetch "https://github.com/$repository/releases/download/$release/$asset" "$archive" || fail "Release $release is unavailable or its build has not finished: https://github.com/$repository/actions"
  fetch "https://github.com/$repository/releases/download/$release/checksums.txt" "$checksums" || fail 'Could not download the release checksum manifest.'
fi
[[ -f $archive && -f $checksums ]] || { echo 'Archive and checksums.txt are required.' >&2; exit 1; }
asset=$(basename -- "$archive")
expected=$(awk -v name="$asset" '$2 == name || $2 == "*"name { print $1 }' "$checksums")
[[ $expected =~ ^[0-9a-fA-F]{64}$ ]] || { echo 'Archive has no unique SHA-256 checksum.' >&2; exit 1; }
actual=$(sha256sum -- "$archive"); actual=${actual%% *}
[[ ${actual,,} == ${expected,,} ]] || { echo 'Checksum mismatch; nothing was installed.' >&2; exit 1; }
# Reject traversal and links before extracting a verified release archive.
tar -tzf "$archive" > "$temporary/members"
while IFS= read -r member; do
  [[ $member != /* && $member != *'..'* && $member != *[[:cntrl:]]* ]] || { echo 'Unsafe archive member.' >&2; exit 1; }
done < "$temporary/members"
if tar -tvzf "$archive" | awk 'substr($1,1,1)!="-" && substr($1,1,1)!="d" {bad=1} END {exit !bad}'; then echo 'Archive links/devices are not allowed.' >&2; exit 1; fi
mkdir "$temporary/unpack"
tar -xzf "$archive" -C "$temporary/unpack" --no-same-owner --no-same-permissions
for item in subbridge install.sh subbridge.sh deploy/subbridge.service deploy/subbridge.openrc; do
  [[ -f $temporary/unpack/$item ]] || { echo "Missing release file: $item" >&2; exit 1; }
done
chmod 755 "$temporary/unpack/subbridge"
"$temporary/unpack/subbridge" -version
mkdir -p -- "$config_dir" "$data_dir"
chmod 700 "$config_dir" "$data_dir"
if ((had_manager)); then cp -p -- "$manager" "$temporary/previous-manager"; fi
if [[ $init == systemd ]]; then
  unit=/etc/systemd/system/subbridge.service
  systemctl is-active --quiet subbridge && was_active=1 || true
  systemctl is-enabled --quiet subbridge && was_enabled=1 || true
elif [[ $init == openrc ]]; then
  unit=/etc/init.d/subbridge
  rc-service subbridge status >/dev/null 2>&1 && was_active=1 || true
fi
if [[ -n $unit && -f $unit ]]; then
  had_unit=1
  cp -p -- "$unit" "$temporary/previous-unit"
fi
new_dir="$app_dir.new.$$"
mkdir -m 755 -- "$new_dir"
install -m 755 "$temporary/unpack/subbridge" "$new_dir/subbridge-core"
install -m 755 "$temporary/unpack/install.sh" "$new_dir/install.sh"
install -m 755 "$temporary/unpack/subbridge.sh" "$new_dir/subbridge.sh"
printf '%s\n' "$repository" > "$new_dir/repository"
channel=stable
if [[ $release == dev || $asset == subbridge-dev-linux-*.tar.gz ]]; then channel=dev; fi
printf '%s\n' "$channel" > "$new_dir/release-channel"
chmod 644 "$new_dir/repository" "$new_dir/release-channel"
# Existing management settings must not be rewritten by an update.
if [[ ! -f $config_dir/config.json || ! -f $config_dir/management.json ]]; then
  if ! "$new_dir/subbridge-core" -config "$config_dir/config.json" -management-config "$config_dir/management.json" -data-dir "$data_dir" -setup > "$temporary/setup.log" 2>&1; then
    cat "$temporary/setup.log" >&2
    fail 'Could not initialize configuration. The existing installation was not stopped.'
  fi
fi
if ! "$new_dir/subbridge-core" -config "$config_dir/config.json" -check; then rm -rf -- "$new_dir"; exit 1; fi
management=$("$new_dir/subbridge-core" -config "$config_dir/config.json" -management-config "$config_dir/management.json" -show-management)
panel_ip=$(sed -nE 's/.*"ip":"([^"]+)".*/\1/p' <<< "$management")
panel_port=$(sed -nE 's/.*"port":([0-9]+).*/\1/p' <<< "$management")
panel_path=$(sed -nE 's/.*"path":"([^"]+)".*/\1/p' <<< "$management")
panel_scheme=http
if [[ $management == *'"tls_cert":'* ]]; then panel_scheme=https; fi
panel_host=$panel_ip
if [[ $panel_host == *:* ]]; then panel_host="[$panel_host]"; fi
if [[ $init != stage ]]; then
  if ! id subbridge >/dev/null 2>&1; then
    if command -v useradd >/dev/null; then useradd --system --user-group --home-dir /var/lib/subbridge --no-create-home --shell /sbin/nologin subbridge
    else addgroup -S subbridge; adduser -S -D -H -h /var/lib/subbridge -s /sbin/nologin -G subbridge subbridge; fi
  fi
  chown -R subbridge:subbridge "$config_dir" "$data_dir"
fi
# Everything needed for rollback is captured before stopping the live service.
backup="$app_dir.previous.$$"
[[ ! -e $backup ]] || fail "Backup path already exists: $backup"
transaction=1
if ((was_active)); then service stop; fi
if ((installed)); then
  mv -- "$app_dir" "$backup"
  app_moved=1
fi
mv -- "$new_dir" "$app_dir"
app_published=1
manager_written=1
install -m 755 "$app_dir/subbridge.sh" "$manager"
if [[ $init == systemd ]]; then
  unit_written=1
  install -m 644 "$temporary/unpack/deploy/subbridge.service" "$unit"
  systemctl daemon-reload
  if ((!installed && enable_boot && !was_enabled)); then
    enable_changed=1
    systemctl enable subbridge
  fi
elif [[ $init == openrc ]]; then
  unit_written=1
  install -m 755 "$temporary/unpack/deploy/subbridge.openrc" "$unit"
  if ((!installed && enable_boot)); then
    enable_changed=1
    rc-update add subbridge default
  fi
fi
if [[ $init != stage ]] && ((was_active || !installed)); then
  service start
  sleep 2
  if [[ $init == systemd ]]; then systemctl is-active --quiet subbridge
  else rc-service subbridge status >/dev/null; fi
fi
committed=1
if ((app_moved)); then rm -rf -- "$backup"; fi
printf '\nSubBridge installed successfully (%s channel).\n' "$channel"
printf 'Console menu: %s\n' "$manager"
if [[ $init != stage ]]; then
  echo 'Credentials: sudo subbridge credentials'
  echo 'Show management settings: sudo subbridge management'
  echo 'Change panel IP or port: sudo subbridge settings'
  if ((installed && !was_active)); then echo 'Service was stopped before the update and remains stopped. Start with: sudo subbridge start'; fi
else
  echo 'Stage mode: no services, users or autostart were changed.'
fi
printf 'Configuration: %s/config.json\n' "$config_dir"
printf 'Management settings: %s/management.json\n' "$config_dir"
printf 'Web panel listener: %s://%s:%s%s\n' "$panel_scheme" "$panel_host" "$panel_port" "$panel_path"
if [[ $panel_ip == 127.* || $panel_ip == ::1 ]]; then
  printf 'SSH tunnel: ssh -N -L %s:%s:%s root@SERVER_IP\n' "$panel_port" "$panel_host" "$panel_port"
fi
