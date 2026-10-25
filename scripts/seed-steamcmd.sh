#!/usr/bin/env bash
set -euo pipefail
source_seed_dest=${1:?Usage: seed-steamcmd.sh DESTINATION}
source_seed_tmp=$(mktemp -d)
trap 'rm -rf -- "$source_seed_tmp"' EXIT
source_seed_url=https://media.steampowered.com/client
download() {
  curl -fsSL --proto '=https' --retry 3 --connect-timeout 15 --max-time 180 "$1" -o "$2"
}
download "$source_seed_url/steam_cmd_linux" "$source_seed_tmp/manifest"
mkdir -p "$source_seed_dest"
manifest_value() {
  awk -v package="$1" -v key="$2" '
    $1 == "\"" package "\"" { selected=1; next }
    selected && $1 == "}" { exit }
    selected && $1 == "\"" key "\"" { gsub(/"/, "", $2); print $2 }
  ' "$source_seed_tmp/manifest"
}
# Download the current Valve packages over HTTPS rather than running the legacy
# tarball bootstrapper. Its update transport can fail on hosted build runners.
for package in steamcmd_public_all steamcmd_bins_linux steamcmd_siteserverui_linux steamcmd_linux; do
  file=$(manifest_value "$package" file)
  checksum=$(manifest_value "$package" sha2)
  if [[ ! $file =~ ^${package}\.zip\.[a-f0-9]{40}$ || ! $checksum =~ ^[a-f0-9]{64}$ ]]; then
    echo "Invalid SteamCMD manifest entry: $package" >&2
    exit 1
  fi
  download "$source_seed_url/$file" "$source_seed_tmp/$file"
  (cd "$source_seed_tmp" && printf '%s  %s\n' "$checksum" "$file" | sha256sum --check -)
  unzip -oq "$source_seed_tmp/$file" -d "$source_seed_dest"
done
test -s "$source_seed_dest/steamcmd.sh"
test -s "$source_seed_dest/linux32/steamcmd"
test -s "$source_seed_dest/linux32/steamclient.so"
test -s "$source_seed_dest/linux64/steamclient.so"
chmod +x "$source_seed_dest/steamcmd.sh" "$source_seed_dest/linux32/steamcmd"
