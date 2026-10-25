#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
# Verify a release, provision host dependencies, then open the Go setup wizard.
set -euo pipefail

release=${SOURCE_NODE_RELEASE:-}
channel=${SOURCE_NODE_CHANNEL:-stable}
repo=${SOURCE_EGG_REPOSITORY:-edgegamers/source-egg}
local_binary=''
headless=false
forward=()
while (($#)); do
  case "$1" in
    --yes) headless=true; forward+=(--yes); shift;;
    --local) [[ $# -ge 2 && -z $local_binary && -f $2 ]] || { echo 'Usage: install-node.sh [--local /path/to/sourcenode] [--yes]' >&2; exit 2; }; local_binary=$(realpath -- "$2"); shift 2;;
    -h|--help) echo 'Usage: install-node.sh [--local /path/to/sourcenode] [--yes]'; exit 0;;
    *) echo "Unknown argument: $1" >&2; exit 2;;
  esac
done
[[ $channel == stable || $channel == beta ]] || { echo 'SOURCE_NODE_CHANNEL must be stable or beta.' >&2; exit 1; }
[[ -z $release || $release =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9.-]+)?$ ]] || { echo 'SOURCE_NODE_RELEASE must be an exact tag such as v1.2.3.' >&2; exit 1; }
[[ $repo =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || { echo 'SOURCE_EGG_REPOSITORY must be owner/repository.' >&2; exit 1; }
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || { echo 'The node requires Linux x86-64.' >&2; exit 1; }
if ! $headless && ! (exec 3<>/dev/tty) 2>/dev/null; then
  echo 'The wizard needs a terminal. Run from an SSH terminal, or pass --yes for saved settings/defaults.' >&2
  exit 1
fi
root=()
if [[ $EUID != 0 ]]; then
  command -v sudo >/dev/null || { echo 'sudo is required when not running as root.' >&2; exit 1; }
  root=(sudo)
fi
command -v dpkg-query >/dev/null && command -v apt-get >/dev/null || { echo 'This bootstrap supports Debian/Ubuntu with apt.' >&2; exit 1; }
scratch=$(mktemp -d)
trap 'rm -rf -- "$scratch"' EXIT
if ! command -v jq >/dev/null; then
  "${root[@]}" apt-get update
  "${root[@]}" apt-get install -y --no-install-recommends jq
fi
if [[ -n $local_binary ]]; then
  cp -- "$local_binary" "$scratch/sourcenode_linux_amd64"
else
  api="https://api.github.com/repos/$repo/releases"
  if [[ -n $release ]]; then endpoint="$api/tags/$release";
  elif [[ $channel == beta ]]; then endpoint="$api?per_page=100";
  else endpoint="$api/latest"; fi
  if ! curl -fsSL -H 'Accept: application/vnd.github+json' "$endpoint" -o "$scratch/releases.json"; then
    echo 'No matching published release could be downloaded. Publish a release and wait for its workflow to succeed, or use --local.' >&2
    exit 1
  fi
  if [[ -z $release && $channel == beta ]]; then
    jq -e 'first(.[] | select(.draft == false))' "$scratch/releases.json" > "$scratch/release.json"
  else cp "$scratch/releases.json" "$scratch/release.json"; fi
  resolved=$(jq -er '.tag_name | select(type == "string")' "$scratch/release.json")
  [[ $resolved =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9.-]+)?$ && ( -z $release || $release == "$resolved" ) ]] || { echo 'GitHub returned an invalid or unexpected release tag.' >&2; exit 1; }
  jq -e '.draft == false and (.prerelease | type) == "boolean" and (.assets | map(.name) | index("sourcenode_linux_amd64") != null)' "$scratch/release.json" >/dev/null || { echo "Release $resolved has no complete node assets. Wait for its workflow to succeed." >&2; exit 1; }
  checksum=$(jq -er '.assets | map(.name) | if index("checksums.txt") != null then "checksums.txt" elif index("sourcenode_linux_amd64.sha256") != null then "sourcenode_linux_amd64.sha256" else error("Release checksum is not available yet") end' "$scratch/release.json")
  if [[ -z $release && $channel == stable && $(jq -r '.prerelease' "$scratch/release.json") != false ]]; then echo 'Stable lookup returned a prerelease.' >&2; exit 1; fi
  [[ $(jq -r '.prerelease' "$scratch/release.json") != true ]] || channel=beta
  release=$resolved
  echo "Installing $release (node release channel: $channel)"
  base="https://github.com/$repo/releases/download/$release"
  curl -fsSL "$base/sourcenode_linux_amd64" -o "$scratch/sourcenode_linux_amd64"
  curl -fsSL "$base/$checksum" -o "$scratch/checksums.txt"
  (
    cd "$scratch"
    # Verify only the fixed expected binary; never execute checksum-file paths.
    awk '($2 == "sourcenode_linux_amd64" || $2 == "*sourcenode_linux_amd64") && length($1) == 64 && $1 !~ /[^0-9a-fA-F]/ {print $1 "  sourcenode_linux_amd64"; count++} END {if(count != 1) exit 1}' checksums.txt > node.sha256
    sha256sum -c node.sha256
  )
fi
chmod 700 "$scratch/sourcenode_linux_amd64"
missing=()
for package in lib32gcc-s1 lib32stdc++6; do
  [[ $(dpkg-query -W -f='${db:Status-Status}' "$package" 2>/dev/null || true) == installed ]] || missing+=("$package")
done
if ((${#missing[@]})); then
  echo "Installing host SteamCMD runtime: ${missing[*]}"
  "${root[@]}" apt-get update
  "${root[@]}" apt-get install -y --no-install-recommends "${missing[@]}"
fi
if $headless; then
  "${root[@]}" "$scratch/sourcenode_linux_amd64" install --channel "$channel" "${forward[@]}" </dev/null
else
  # curl | bash consumes stdin; the wizard must read the controlling terminal.
  "${root[@]}" "$scratch/sourcenode_linux_amd64" install --channel "$channel" "${forward[@]}" </dev/tty
fi
