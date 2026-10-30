#!/usr/bin/env bash
set -euo pipefail
source_egg_image=${1:-source-egg:dev}
# Simulate Wings' read-only root and arbitrary configured UID without Steam/game downloads.
docker run --rm -i --read-only --user 988:988 \
  --tmpfs /home/container:rw,exec,uid=988,gid=988,mode=0700 \
  --tmpfs /tmp:rw,mode=1777,size=100m \
  -e SOURCE_GAME=CS:S -e SOURCE_MODE=standalone -e NODE_WAIT_SECONDS=0 \
  -e AUTO_UPDATE=0 -e INSTALL_METAMOD=0 -e INSTALL_SOURCEMOD=0 \
  --entrypoint /bin/bash "$source_egg_image" -s <<'SMOKE'
set -euo pipefail
check_curl_runtime() {
  local triplet=$1 expected_header=$2 library header dependencies
  library="/usr/lib/$triplet/libcurl-gnutls.so.4"
  if [[ ! -f $library ]]; then
    echo "Missing GnuTLS curl runtime: $library" >&2
    exit 1
  fi
  header=$(od -An -tx1 -N5 "$library" | tr -d '[:space:]')
  if [[ $header != "$expected_header" ]]; then
    echo "Unexpected ELF class for $library: $header" >&2
    exit 1
  fi
  if ! dependencies=$(ldd "$library" 2>&1); then
    printf '%s\n' "$dependencies" >&2
    exit 1
  fi
  if [[ $dependencies == *'not found'* ]]; then
    printf 'Unresolved dependencies for %s:\n%s\n' "$library" "$dependencies" >&2
    exit 1
  fi
}
# TF2's 64-bit replay module needs GnuTLS curl; OpenSSL curl cannot replace it.
check_curl_runtime x86_64-linux-gnu 7f454c4602
check_curl_runtime i386-linux-gnu 7f454c4601
echo '32-bit and 64-bit GnuTLS curl ELF/dependency smoke passed'
cd /home/container
mkdir -p cstrike steamapps
printf 'game\n' > cstrike/gameinfo.txt
printf '"appid" "232330" "buildid" "100" "StateFlags" "4"\n' > steamapps/appmanifest_232330.acf
cat > srcds_run <<'GAME'
#!/bin/bash
echo 'fixture srcds process started'
while read -r command; do
  if [[ $command == quit ]]; then exit 0; fi
done
GAME
chmod +x srcds_run
printf 'quit\n' > /tmp/commands
/usr/local/bin/sourceegg < /tmp/commands
test -w .source/steamcmd/steamcmd.sh
test -f .steam/sdk32/steamclient.so
test -f .source/identity.json
echo 'Read-only root/arbitrary UID/SteamCMD seed/PTY boot smoke passed'
SMOKE
