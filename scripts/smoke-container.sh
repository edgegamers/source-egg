#!/usr/bin/env bash
set -euo pipefail
source_egg_image=${1:-source-egg:dev}
# Simulate Wings' read-only root and arbitrary configured UID without Steam/game downloads.
docker run --rm -i --read-only --user 988:988 \
  --tmpfs /home/container:rw,exec,uid=988,gid=988,mode=0700 \
  --tmpfs /tmp:rw,mode=1777 \
  -e SOURCE_GAME=CS:S -e SOURCE_MODE=standalone -e NODE_WAIT_SECONDS=0 \
  -e AUTO_UPDATE=0 -e INSTALL_METAMOD=0 -e INSTALL_SOURCEMOD=0 \
  --entrypoint /bin/bash "$source_egg_image" -s <<'SMOKE'
set -euo pipefail
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
