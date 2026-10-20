#!/usr/bin/env bash
# Exercise release selection and publishing with local curl/gh fixtures only.
set -euo pipefail
temporary=$(mktemp -d)
trap 'rm -rf -- "$temporary"' EXIT
version=${SUBBRIDGE_VERSION:-1.1.0}
archive="$PWD/dist/releases/subbridge-${SUBBRIDGE_RELEASE_TAG:-v$version}-linux-amd64.tar.gz"
fixture="$temporary/fixture"
mkdir -p "$temporary/bin" "$fixture/dev" "$fixture/v9.8.7" "$fixture/publish"
for tag in dev v9.8.7; do
  name="subbridge-$tag-linux-amd64.tar.gz"
  cp -- "$archive" "$fixture/$tag/$name"
  hash=$(sha256sum "$fixture/$tag/$name"); hash=${hash%% *}
  printf '%s  %s\n' "$hash" "$name" > "$fixture/$tag/checksums.txt"
done
cat > "$temporary/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
output= url=
while (($#)); do
  case "$1" in --output) output=$2; shift 2;; https://*) url=$1; shift;; *) shift;; esac
done
printf '%s\n' "$url" >> "$SUBBRIDGE_TEST_FIXTURE/fetches"
case "$url" in
  https://api.github.com/repos/example/subbridge/releases/latest) printf '{"tag_name":"v9.8.7"}\n' > "$output";;
  https://github.com/example/subbridge/releases/download/*)
    relative=${url#https://github.com/example/subbridge/releases/download/}
    cp -- "$SUBBRIDGE_TEST_FIXTURE/$relative" "$output";;
  *) echo "Unexpected network request: $url" >&2; exit 1;;
esac
EOF
cat > "$temporary/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s ' "$@" >> "$SUBBRIDGE_TEST_FIXTURE/gh-calls"
printf '\n' >> "$SUBBRIDGE_TEST_FIXTURE/gh-calls"
if [[ $1 == api ]]; then
  if [[ ${2:-} == --method ]]; then touch "$SUBBRIDGE_TEST_FIXTURE/tag"; exit 0; fi
  [[ -f $SUBBRIDGE_TEST_FIXTURE/tag ]]
elif [[ $1 == release && $2 == view ]]; then
  [[ -f $SUBBRIDGE_TEST_FIXTURE/release-$3 ]]
elif [[ $1 == release && $2 == create ]]; then
  touch "$SUBBRIDGE_TEST_FIXTURE/release-$3"
elif [[ $1 != release || ( $2 != edit && $2 != upload ) ]]; then
  echo 'Unexpected gh command.' >&2; exit 1
fi
EOF
chmod 755 "$temporary/bin/curl" "$temporary/bin/gh"
export PATH="$temporary/bin:$PATH" SUBBRIDGE_TEST_FIXTURE="$fixture"
stage="$temporary/root"
install_args=(--repo example/subbridge --stage-dir "$stage")
channel="$stage/usr/local/lib/subbridge/release-channel"
bash install.sh -dev "${install_args[@]}"
[[ $(cat "$channel") == dev ]]
! grep -Fq '/releases/latest' "$fixture/fetches"
SUBBRIDGE_ROOT="$stage" bash "$stage/usr/local/bin/subbridge" url >/dev/null
before=$(sha256sum "$stage/etc/subbridge/config.json" "$stage/etc/subbridge/management.json" "$stage/var/lib/subbridge/token")
SUBBRIDGE_ROOT="$stage" bash "$stage/usr/local/bin/subbridge" update
[[ $(cat "$channel") == dev ]]
! grep -Fq '/releases/latest' "$fixture/fetches"
SUBBRIDGE_ROOT="$stage" bash "$stage/usr/local/bin/subbridge" update --version latest
[[ $(cat "$channel") == stable ]]
grep -Fq '/releases/latest' "$fixture/fetches"
SUBBRIDGE_ROOT="$stage" bash "$stage/usr/local/bin/subbridge" update -dev
[[ $(cat "$channel") == dev ]]
[[ $(sha256sum "$stage/etc/subbridge/config.json" "$stage/etc/subbridge/management.json" "$stage/var/lib/subbridge/token") == "$before" ]]
# A local archive determines its channel; the manager must not inject -dev.
SUBBRIDGE_ROOT="$stage" bash "$stage/usr/local/bin/subbridge" update --archive "$fixture/dev/subbridge-dev-linux-amd64.tar.gz" --checksums "$fixture/dev/checksums.txt"
[[ $(cat "$channel") == dev ]]
# Selection errors and bad dev checksums cannot replace an existing installation.
if bash install.sh -dev --version v9.8.7 "${install_args[@]}"; then echo 'Conflicting release options were accepted.' >&2; exit 1; fi
printf '%064d  subbridge-dev-linux-amd64.tar.gz\n' 0 > "$fixture/dev/checksums.txt"
if bash install.sh --version dev "${install_args[@]}"; then echo 'Invalid dev checksum was accepted.' >&2; exit 1; fi
[[ $(cat "$channel") == dev ]]
[[ $(sha256sum "$stage/etc/subbridge/config.json" "$stage/etc/subbridge/management.json" "$stage/var/lib/subbridge/token") == "$before" ]]

for target in linux-amd64 linux-arm64 linux-armv7 linux-386 windows-amd64 windows-arm64 source; do
  extension=tar.gz
  if [[ $target == windows-* ]]; then extension=zip; fi
  printf 'synthetic release asset: %s\n' "$target" > "$fixture/publish/subbridge-dev-$target.$extension"
done
(cd "$fixture/publish" && sha256sum subbridge-* > checksums.txt)
export GH_REPO=example/subbridge GH_TOKEN=synthetic-ci-token
commit=0123456789abcdef0123456789abcdef01234567
bash scripts/publish-release.sh "$fixture/publish" dev "$commit"
grep -Fq -- '--method POST' "$fixture/gh-calls"
grep -Fq -- '--prerelease --latest=false' "$fixture/gh-calls"
grep -Fq 'release create dev' "$fixture/gh-calls"
bash scripts/publish-release.sh "$fixture/publish" dev "$commit"
grep -Fq -- '--method PATCH' "$fixture/gh-calls"
grep -Fq 'release edit dev' "$fixture/gh-calls"
[[ $(tail -n 1 "$fixture/gh-calls") == 'release upload dev checksums.txt --clobber ' ]]
for file in "$fixture/publish"/subbridge-dev-*; do cp -- "$file" "${file/subbridge-dev-/subbridge-v9.8.7-}"; done
(cd "$fixture/publish" && sha256sum subbridge-v9.8.7-* > checksums.txt)
bash scripts/publish-release.sh "$fixture/publish" v9.8.7 "$commit"
grep -Fq 'release create v9.8.7' "$fixture/gh-calls"
grep -Fq -- '--verify-tag' "$fixture/gh-calls"
echo 'Offline dev/stable installation, channel updates, checksum rejection and release publishing passed.'
