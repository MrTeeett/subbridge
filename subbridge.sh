#!/usr/bin/env bash
# SubBridge console manager. This file is installed as /usr/local/bin/subbridge.
set -euo pipefail
root=${SUBBRIDGE_ROOT:-}
app_dir="$root/usr/local/lib/subbridge"
config_dir="$root/etc/subbridge"
core="$app_dir/subbridge-core"
options=(-config "$config_dir/config.json" -management-config "$config_dir/management.json")
exit_after_uninstall=0
require_root() { [[ $(id -u) == 0 || -n $root ]] || { echo 'Run with sudo.' >&2; return 1; }; }
init_system() {
  if [[ -d /run/systemd/system ]] && command -v systemctl >/dev/null; then echo systemd
  elif command -v rc-service >/dev/null; then echo openrc
  else echo 'No systemd/OpenRC found.' >&2; return 1; fi
}
service_action() {
  require_root || return
  local init
  init=$(init_system) || return
  case "$init" in
    systemd) systemctl "$1" subbridge;;
    openrc) rc-service subbridge "$1";;
  esac
}
autostart() {
  require_root || return
  local init
  init=$(init_system) || return
  case "$init" in
    systemd) systemctl "$1" subbridge;;
    openrc) if [[ $1 == enable ]]; then rc-update add subbridge default; else rc-update del subbridge default; fi;;
  esac
}
settings() {
  require_root || return
  local ip port name
  read -r -p 'Management IP [127.0.0.1]: ' ip || return
  read -r -p 'Management port [8780]: ' port || return
  read -r -p 'Username [keep current]: ' name || return
  local args=(-admin-ip "${ip:-127.0.0.1}" -admin-port "${port:-8780}")
  if [[ -n $name ]]; then args+=(-admin-username "$name"); fi
  "$core" "${options[@]}" "${args[@]}" || return
  if [[ -z $root ]]; then
    chown subbridge:subbridge "$config_dir"/* || return
    service_action restart || return
  fi
}
reset_admin() {
  require_root || return
  "$core" "${options[@]}" -reset-admin || return
  if [[ -z $root ]]; then
    chown subbridge:subbridge "$config_dir"/* || return
    service_action restart || return
  fi
  echo "Read new credentials with: subbridge credentials"
}
update() {
  require_root || return
  local repo argument
  local channel_args=()
  repo=$(cat "$app_dir/repository") || return
  if [[ -f $app_dir/release-channel ]] && [[ $(cat "$app_dir/release-channel") == dev ]]; then
    channel_args=(-dev)
    for argument in "$@"; do
      case "$argument" in -dev|--dev|--version|--archive) channel_args=(); break;; esac
    done
  fi
  if [[ -n $root ]]; then bash "$app_dir/install.sh" --repo "$repo" --stage-dir "$root" "${channel_args[@]}" "$@"
  else bash "$app_dir/install.sh" --repo "$repo" "${channel_args[@]}" "$@"; fi
}
uninstall() {
  require_root || return
  local answer
  if [[ ${1:-} != --yes ]]; then
    read -r -p 'Remove SubBridge binaries and services? Config/data will be kept. [y/N] ' answer || return
    [[ $answer == y || $answer == Y ]] || return 0
  fi
  if [[ -z $root ]]; then
    local init
    init=$(init_system) || return
    case "$init" in
      systemd) systemctl disable --now subbridge || true; rm -f /etc/systemd/system/subbridge.service || return; systemctl daemon-reload || return;;
      openrc) rc-service subbridge stop || true; rc-update del subbridge default || true; rm -f /etc/init.d/subbridge || return;;
    esac
  fi
  rm -rf -- "$app_dir" || return
  rm -f -- "$root/usr/local/bin/subbridge" || return
  echo "Removed. Config kept in $config_dir; data kept in $root/var/lib/subbridge."
  exit_after_uninstall=1
}
execute() {
  local command=${1:-help}; shift || true
  case "$command" in
    start|stop|restart|status) service_action "$command";;
    enable|disable) autostart "$command";;
    logs)
      local init
      init=$(init_system) || return
      if [[ $init == systemd ]]; then journalctl -u subbridge -n 100 --no-pager "$@"; else tail -n 100 "$root/var/log/subbridge.log"; fi;;
    update|install) update "$@";;
    uninstall) uninstall "$@";;
    settings) settings;;
    reset-admin) reset_admin;;
    credentials) require_root || return; if [[ -f $config_dir/setup-credentials.txt ]]; then cat "$config_dir/setup-credentials.txt"; else echo 'Initial credentials were removed after a password change. Use reset-admin if needed.'; fi;;
    management) require_root || return; "$core" "${options[@]}" -show-management;;
    url) require_root || return; "$core" "${options[@]}" -print-url;;
    check) require_root || return; "$core" "${options[@]}" -check;;
    version) "$core" -version;;
    run) exec "$core" "${options[@]}" "$@";;
    help|-h|--help) echo 'subbridge [start|stop|restart|status|logs|update|uninstall|enable|disable|settings|reset-admin|credentials|management|url|check|version|run]';;
    *) echo "Unknown command: $command" >&2; return 2;;
  esac
}
if (($#)); then execute "$@"; exit; fi
if [[ ! -t 0 ]]; then execute help; exit; fi
while true; do
  cat <<'EOF'

  SubBridge
   1. Status
   2. Start
   3. Stop
   4. Restart
   5. Logs
   6. Update
   7. Enable autostart
   8. Disable autostart
   9. Panel settings
  10. Reset password
  11. Credentials
  12. Subscription URL
  13. Uninstall
   0. Exit
EOF
  read -r -p '> ' choice || exit 0
  case "$choice" in
    0) exit 0;; 1) command=status;; 2) command=start;; 3) command=stop;; 4) command=restart;;
    5) command=logs;; 6) command=update;; 7) command=enable;; 8) command=disable;;
    9) command=settings;; 10) command=reset-admin;; 11) command=credentials;; 12) command=url;; 13) command=uninstall;;
    *) continue;;
  esac
  execute "$command" || echo 'Command failed; returning to the menu.' >&2
  if ((exit_after_uninstall)); then exit 0; fi
done
