#!/usr/bin/env bash
# Integration check: no root, actual init changes or network access are required.
set -euo pipefail
temporary=$(mktemp -d)
trap 'rm -rf -- "$temporary"' EXIT
version=${SUBBRIDGE_VERSION:-1.1.0}
archive="$PWD/dist/releases/subbridge-${SUBBRIDGE_RELEASE_TAG:-v$version}-linux-amd64.tar.gz"
checksums="$PWD/dist/releases/checksums.txt"
stage="$temporary/root"
bash install.sh --archive "$archive" --checksums "$checksums" --stage-dir "$stage"
config="$stage/etc/subbridge/config.json"
management="$stage/etc/subbridge/management.json"
core="$stage/usr/local/lib/subbridge/subbridge-core"
[[ -x $core && -x $stage/usr/local/bin/subbridge ]]
[[ $(stat -c %a "$management") == 600 && $(stat -c %a "$stage/etc/subbridge") == 700 ]]
before=$(sha256sum "$config" "$management" "$stage/etc/subbridge/setup-credentials.txt")
printf 'synthetic retained state\n' > "$stage/var/lib/subbridge/install-test"
bash install.sh --archive "$archive" --checksums "$checksums" --stage-dir "$stage"
after=$(sha256sum "$config" "$management" "$stage/etc/subbridge/setup-credentials.txt")
[[ $before == "$after" && -f $stage/var/lib/subbridge/install-test ]]
SUBBRIDGE_ROOT="$stage" bash "$stage/usr/local/bin/subbridge" check
SUBBRIDGE_ROOT="$stage" bash "$stage/usr/local/bin/subbridge" version
printf '%064d  %s\n' 0 "$(basename "$archive")" > "$temporary/wrong-checksums"
if bash install.sh --archive "$archive" --checksums "$temporary/wrong-checksums" --stage-dir "$stage"; then echo 'Invalid checksum was accepted.' >&2; exit 1; fi
[[ $(sha256sum "$config" "$management" "$stage/etc/subbridge/setup-credentials.txt") == "$before" ]]
for option in --repo --version --archive --checksums --stage-dir; do
  if bash install.sh "$option"; then echo "Missing value for $option was accepted." >&2; exit 1; fi
done
if bash install.sh --archive "$archive" --stage-dir "$stage"; then echo 'A local archive without checksums was accepted.' >&2; exit 1; fi
[[ $(sha256sum "$config" "$management" "$stage/etc/subbridge/setup-credentials.txt") == "$before" ]]
SUBBRIDGE_ROOT="$stage" bash "$stage/usr/local/bin/subbridge" uninstall --yes
[[ ! -e $core && -f $config && -f $stage/var/lib/subbridge/install-test ]]
echo 'Staged install, update, checksum rejection and uninstall passed.'
