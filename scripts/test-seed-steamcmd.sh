#!/usr/bin/env bash
set -euo pipefail
source_seed_repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
source_seed_tmp=$(mktemp -d)
trap 'rm -rf -- "$source_seed_tmp"' EXIT
mkdir -p "$source_seed_tmp/bin" "$source_seed_tmp/fixtures"
printf '"linux"\n{\n' > "$source_seed_tmp/fixtures/manifest"
for package in steamcmd_public_all steamcmd_bins_linux steamcmd_siteserverui_linux steamcmd_linux; do
  file="$package.zip.0123456789abcdef0123456789abcdef01234567"
  printf 'fixture %s\n' "$package" > "$source_seed_tmp/fixtures/$file"
  checksum=$(sha256sum "$source_seed_tmp/fixtures/$file" | cut -d ' ' -f 1)
  printf '"%s"\n{\n"file" "%s"\n"sha2" "%s"\n}\n' "$package" "$file" "$checksum" >> "$source_seed_tmp/fixtures/manifest"
done
printf '}\n' >> "$source_seed_tmp/fixtures/manifest"
cat > "$source_seed_tmp/bin/curl" <<'CURL'
#!/usr/bin/env bash
set -euo pipefail
out=''; url=''
while (($#)); do case "$1" in -o) out=$2;shift 2;; --retry|--proto|--connect-timeout|--max-time) shift 2;; -*) shift;; *) url=$1;shift;; esac;done
printf '%s\n' "$url" >> "$FIXTURE_LOG"
case "$url" in
  https://media.steampowered.com/client/steam_cmd_linux) cp "$FIXTURE_DIR/manifest" "$out";;
  https://media.steampowered.com/client/steamcmd_*.zip.*) cp "$FIXTURE_DIR/${url##*/}" "$out";;
  *) exit 2;;
esac
CURL
cat > "$source_seed_tmp/bin/unzip" <<'UNZIP'
#!/usr/bin/env bash
set -euo pipefail
archive=$2; dest=$4
printf '%s\n' "${archive##*/}" >> "$EXTRACT_LOG"
case "${archive##*/}" in
  steamcmd_public_all.zip.*)
    printf '#!/bin/sh\necho "Updater must not run during image build" >&2\nexit 254\n' > "$dest/steamcmd.sh";;
  steamcmd_bins_linux.zip.*)
    mkdir -p "$dest/linux32" "$dest/linux64"
    printf client > "$dest/linux32/steamclient.so"
    printf client > "$dest/linux64/steamclient.so";;
  steamcmd_linux.zip.*)
    mkdir -p "$dest/linux32"
    printf executable > "$dest/linux32/steamcmd";;
esac
UNZIP
chmod +x "$source_seed_tmp/bin/"*
run_seed() {
  PATH="$source_seed_tmp/bin:$PATH" FIXTURE_DIR="$source_seed_tmp/fixtures" \
    FIXTURE_LOG="$source_seed_tmp/download.log" EXTRACT_LOG="$source_seed_tmp/extract.log" \
    bash "$source_seed_repo/scripts/seed-steamcmd.sh" "$source_seed_tmp/output" > "$source_seed_tmp/log" 2>&1
}
if ! run_seed; then cat "$source_seed_tmp/log";exit 1;fi
test -x "$source_seed_tmp/output/steamcmd.sh"
test -x "$source_seed_tmp/output/linux32/steamcmd"
test -s "$source_seed_tmp/output/linux32/steamclient.so"
test -s "$source_seed_tmp/output/linux64/steamclient.so"
test "$(wc -l < "$source_seed_tmp/extract.log")" -eq 4
test "$(wc -l < "$source_seed_tmp/download.log")" -eq 5

# A modified download must fail before it reaches the extractor.
rm "$source_seed_tmp/extract.log"
printf tampered > "$source_seed_tmp/fixtures/steamcmd_public_all.zip.0123456789abcdef0123456789abcdef01234567"
if run_seed; then echo 'Accepted modified Steam package' >&2;exit 1;fi
test ! -e "$source_seed_tmp/extract.log"
grep -Fq 'FAILED' "$source_seed_tmp/log"

# Reject incomplete manifests and unsafe filenames before package downloads.
printf '"linux"\n{\n}\n' > "$source_seed_tmp/fixtures/manifest"
if run_seed; then echo 'Accepted incomplete Steam manifest' >&2;exit 1;fi
grep -Fq 'Invalid SteamCMD manifest' "$source_seed_tmp/log"
printf '"steamcmd_public_all"\n{\n"file" "../../bad.zip"\n"sha2" "%064d"\n}\n' 0 > "$source_seed_tmp/fixtures/manifest"
if run_seed; then echo 'Accepted unsafe Steam package path' >&2;exit 1;fi
grep -Fq 'Invalid SteamCMD manifest' "$source_seed_tmp/log"
echo 'SteamCMD seed fixtures passed: HTTPS packages, checksums, no updater execution, invalid manifests'
