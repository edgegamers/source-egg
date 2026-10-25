#!/usr/bin/env bash
set -euo pipefail

# Run on a Linux Wings node. --local accepts an already built node binary.
[[ $(uname -s) == Linux && $(uname -m) == x86_64 ]] || { echo 'Linux x86_64 required' >&2; exit 1; }
[[ $EUID == 0 ]] || { echo 'Run with sudo on the Wings node' >&2; exit 1; }
source_node_binary=''
source_node_tmp=$(mktemp -d)
trap 'rm -rf -- "$source_node_tmp"' EXIT
if [[ ${1:-} == --local ]]; then
  [[ $# == 2 && -f $2 ]] || { echo 'usage: install-node.sh --local /path/to/sourcenode' >&2; exit 1; }
  source_node_binary=$(realpath -- "$2")
fi
command -v apt-get >/dev/null || { echo 'Bootstrap supports Debian/Ubuntu; install dependencies manually on other systems' >&2; exit 1; }
apt-get update
apt-get install -y ca-certificates curl jq lib32gcc-s1 lib32stdc++6
if [[ -z $source_node_binary ]]; then
  source_node_repo=${SOURCE_EGG_REPOSITORY:-edgegamers/source-egg}
  source_node_channel=${SOURCE_NODE_CHANNEL:-stable}
  [[ $source_node_channel == stable || $source_node_channel == beta ]] || { echo 'Channel must be stable or beta' >&2; exit 1; }
  source_node_tag=${SOURCE_NODE_RELEASE:-}
  if [[ -z $source_node_tag ]]; then
    source_node_tag=$(curl -fsSL "https://api.github.com/repos/$source_node_repo/releases?per_page=100" | jq -r --arg channel "$source_node_channel" '[.[] | select(.draft == false) | select($channel == "beta" or .prerelease == false)][0].tag_name // empty')
  fi
  [[ -n $source_node_tag ]] || { echo 'No published release; build locally and use --local' >&2; exit 1; }
  curl -fsSL "https://github.com/$source_node_repo/releases/download/$source_node_tag/sourcenode_linux_amd64" -o "$source_node_tmp/sourcenode_linux_amd64"
  curl -fsSL "https://github.com/$source_node_repo/releases/download/$source_node_tag/sourcenode_linux_amd64.sha256" -o "$source_node_tmp/sourcenode_linux_amd64.sha256"
  (cd "$source_node_tmp" && sha256sum -c sourcenode_linux_amd64.sha256)
  source_node_binary=$source_node_tmp/sourcenode_linux_amd64
fi
mkdir -p /opt/steamcmd /etc/sourcenode /var/lib/sourcenode /var/cache/sourcenode /srv/source-shared
chmod 700 /etc/sourcenode /var/lib/sourcenode
if [[ ! -f /opt/steamcmd/steamcmd.sh ]]; then
  curl -fsSL https://steamcdn-a.akamaihd.net/client/installer/steamcmd_linux.tar.gz -o "$source_node_tmp/steamcmd.tar.gz"
  tar -xzf "$source_node_tmp/steamcmd.tar.gz" -C /opt/steamcmd
fi
install -m 0755 "$source_node_binary" /usr/local/bin/sourcenode
if [[ ! -f /etc/sourcenode/config.json ]]; then sourcenode init; fi
cat > /etc/systemd/system/sourcenode.service <<'UNIT'
[Unit]
Description=Source Egg node service
After=docker.service network-online.target
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
ExecStart=/usr/local/bin/sourcenode serve
Restart=on-failure
RestartSec=5
UMask=0022

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now sourcenode
echo 'Installed. Edit /etc/sourcenode/config.json, then systemctl restart sourcenode.'
echo 'Generated join passwords are opt-in; default node sharing scope is game.'
