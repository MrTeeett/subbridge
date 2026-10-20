#!/usr/bin/env bash
# Called by GitHub Actions after all release checks have passed.
set -euo pipefail
directory=${1:?Release directory required}
tag=${2:?Release tag required}
commit=${3:?Commit SHA required}
[[ $tag == dev || $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][a-zA-Z0-9.-]+)?$ ]] || { echo 'Invalid release tag.' >&2; exit 1; }
[[ $commit =~ ^[0-9a-f]{40}$ ]] || { echo 'Invalid commit SHA.' >&2; exit 1; }
: "${GH_REPO:?Repository required}" "${GH_TOKEN:?GitHub token required}"
cd -- "$directory"
sha256sum -c checksums.txt
archives=(subbridge-"$tag"-*.tar.gz subbridge-"$tag"-*.zip)
for archive in "${archives[@]}"; do [[ -f $archive ]] || { echo 'Release archives are missing.' >&2; exit 1; }; done

if [[ $tag == dev ]]; then
  notes=$(mktemp)
  trap 'rm -f -- "$notes"' EXIT
  printf 'Development build from commit `%s`.\n\nInstall with `bash install.sh -dev`. This prerelease is replaced after each successful branch build.\n' "$commit" > "$notes"
  # Updating release metadata alone does not move an existing Git tag.
  if gh api "repos/$GH_REPO/git/ref/tags/dev" >/dev/null 2>&1; then
    gh api --method PATCH "repos/$GH_REPO/git/refs/tags/dev" -f "sha=$commit" -F force=true >/dev/null
  else
    gh api --method POST "repos/$GH_REPO/git/refs" -f ref=refs/tags/dev -f "sha=$commit" >/dev/null
  fi
  if gh release view dev >/dev/null 2>&1; then
    gh release edit dev --title 'SubBridge dev' --prerelease --latest=false --notes-file "$notes"
    gh release upload dev "${archives[@]}" --clobber
    # Upload the manifest last: a partial update must fail checksum verification.
    gh release upload dev checksums.txt --clobber
  else
    gh release create dev "${archives[@]}" checksums.txt --target "$commit" --title 'SubBridge dev' --prerelease --latest=false --notes-file "$notes"
  fi
elif gh release view "$tag" >/dev/null 2>&1; then
  gh release upload "$tag" "${archives[@]}" --clobber
  gh release upload "$tag" checksums.txt --clobber
else
  gh release create "$tag" "${archives[@]}" checksums.txt --verify-tag --title "SubBridge $tag" --generate-notes
fi
