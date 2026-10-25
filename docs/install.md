# Installation and operations

## Requirements

Linux amd64, Docker and Pelican/Pterodactyl Wings. Node-managed mounts require kernel 5.12+ and a root host service. The image includes 32-bit and 64-bit game/SteamCMD dependencies. Go 1.27+ is needed for source builds, not published binaries.

Current standalone CS:GO uses download app 740 and client/auth app 4465480. Its exact Steam authentication/client recipe still needs live acceptance. Do not substitute CS2 app 730. CS:GO metadata is reapplied after install/update; use a GSLT for the standalone client app.

## Install a server

For shared files, [set up the optional Wings node](node.md) first. No node is required for standalone operation.

1. Build/publish a dev image or wait for a successful release workflow.
2. In Pelican's admin importer, use `https://raw.githubusercontent.com/edgegamers/source-egg/main/pterodactyl/source-egg.json`. For Pterodactyl, download/import the JSON file. Verify dropdown rendering on the target panel; the runtime validates choices independently.

   The export embeds a square 512 × 512 Source-logo PNG as the Pelican egg icon. Updating/reimporting the egg applies the picture on panels supporting embedded icons. For manual upload, use `assets/source-logo.png`; older Pterodactyl panels may ignore this visual metadata.
3. Choose the game and branch before first start. Changes on an existing volume fail with a new-volume/reinstall instruction.
4. Set map/hostname/player settings and applicable Steam credentials/GSLT. Empty map uses a game-specific default. Empty Steam username uses anonymous login; authenticated branches need a pre-authorized session for unattended Steam Guard use.
5. Start. Game updates run before the process. Metamod stable and SourceMod prerelease are checked at startup. Exact pins use full upstream tags, such as `1.12.0.1228` and `1.13.0.7478`.

SteamCMD is seeded into `/home/container/.source/steamcmd` so it can self-update under Wings' read-only image root and arbitrary UID. Framework configs/data remain private. Unmodified stock plugins/extensions upgrade; custom edits and disabled/deleted defaults are preserved.

`INSTALL_SOURCEMOD=1` requires Metamod even if its separate toggle is off. Toggles control installation/update; they do not uninstall existing frameworks.

## Optional node

The normal release install uses the same bootstrap/wizard flow as the reference egg:

```bash
curl -fsSL https://raw.githubusercontent.com/edgegamers/source-egg/main/scripts/install-node.sh | bash
sudo sourcenode doctor
sudo sourcenode status
journalctl -u sourcenode -f
```

Wait for the release assets/image before running it. The bootstrap handles sudo, verifies the release and opens the Go wizard; no local build is required. `SOURCE_NODE_CHANNEL=beta` includes prereleases; `SOURCE_NODE_RELEASE=vX.Y.Z` selects an initial tag. Rerun the bootstrap to upgrade the binary, or `sudo sourcenode install` to change saved choices. [Full node guide](node.md) · [Local source builds](development.md).

The daemon discovers exact approved image repositories through Docker, verifies each selected profile, and mounts a separate broker socket, base-game store and framework cache read-only. Containers never receive Docker or root control sockets. Boot waits up to 20 seconds for discovery; first downloads can take longer. `NODE_WAIT_SECONDS` can adjust the discovery wait (0–300) for testing.

The wizard writes `/etc/sourcenode/config.json` and enables/restarts the service. Advanced edits can be made directly, followed by a service restart. Relevant defaults:

```json
{
  "node_channel": "stable",
  "prefetch_games": [],
  "shared": true,
  "framework_cache": true,
  "poll_seconds": 5,
  "check_minutes": 30,
  "restart": {
    "mode": "empty",
    "window": "",
    "max_delay_minutes": 60,
    "warn_seconds": 60
  },
  "password_scope": "game",
  "password_interval_hours": 168,
  "webhook": "",
  "password_webhook": ""
}
```

The excerpt omits path/image/Steam settings. Set `wings_config` for nonstandard config locations; otherwise the daemon checks `/etc/pelican/config.yml` and `/etc/pterodactyl/config.yml`. HTTPS restarts pin the certificate configured in Wings.

Restart policies are `immediate`, `empty`, and `window`. Windows use `HH:MM-HH:MM` in the node's local timezone and wait for that window even when empty. A nonzero maximum delay can override waiting; zero disables that deadline. Unknown player counts are never treated as empty. Local A2S counts exclude bots.

`SOURCE_MODE=standalone` keeps private game files while still allowing node caching/passwords. `managed` requires sharing; `auto` chooses on first start. An already managed volume refuses unsafe local SteamCMD fallback. Materializing it back to standalone is not automated.

## Shared content and rollback

Complete builds live under `/srv/source-shared/generations/<game>/<branch>/<runtime>/<build-token>`. Updates use private staging copies, validate the install and publish fresh generations. Peak disk use includes retained builds plus staging; unchanged remote build IDs avoid staging copies.

Running servers keep their concrete VPK targets until restart. Server configs, addons, logs, gameinfo and event content remain private. Event maps are ordinary uploads; no Workshop acquisition or map cache is provided.

```bash
sudo sourcenode update css
sudo sourcenode history css
sudo sourcenode pin SERVER_UUID generations/css/public/linux-auto/BUILD_TOKEN
sudo sourcenode unpin SERVER_UUID
sudo sourcenode rollback css generations/css/public/linux-auto/OLDER_BUILD_TOKEN
sudo sourcenode unpin-game css
sudo sourcenode prune
```

`pin` affects one server; `rollback` selects/pins a retained build for that game/branch/runtime. Restarts follow the game-update policy; per-server pins override game selections. `unpin`/`unpin-game` resume latest. Missing pins fail rather than selecting latest. Stopped servers remain stopped.

Pruning protects current/applied/pending/pinned builds. Removed-server references are retained conservatively; orphan-reference cleanup is not automated. Automatic canary/health rollback is not implemented; use inspected generations and manual rollback.

Framework downloads are cached centrally and applied at startup. Missing exact framework pins fail. Old archives remain available; automatic archive retention and proactive framework-only restart scheduling are not implemented yet.

## Passwords

Generated passwords apply to `sv_password`, not RCON. Enable `GENERATE_PASSWORD=1` on participating dev servers. Node default `password_scope` is `game`.

| Scope | Sharing boundary |
| --- | --- |
| `game` | Same game on the same node, among participating servers |
| `server` | One server UUID |
| `node` | Participating servers across games on this node |

Admin-only `PASSWORD_SCOPE=default` follows node policy; `game`, `server` and `node` override it per server. No sharing crosses nodes. Assignments persist privately and contain 18 cryptographically random bytes encoded as 24 URL-safe characters. Rotation defaults to 168 hours; zero retains assignments without expiry.

The first query after expiry regenerates the scope assignment. Each running participant detects the change and requests its own Wings restart; stopped servers apply it on next startup. Password rotation is separate from game-update windows. Generated-password startup fails closed without a node assignment.

```bash
sudo sourcenode passwords show FULL_SERVER_UUID
```

Ordinary `webhook` events exclude credentials. Optional `password_webhook` sends actual join passwords to a separately configured private Discord channel and retries unconfirmed deliveries on subsequent assignment checks. Root can reveal credentials explicitly. `RCON_PASSWORD` remains independent.

Disabling generation makes `SERVER_PASSWORD` authoritative; an empty manual value means an open server. Live console overrides remain until startup reapplies configured values and are redacted from saved logs.

## Diagnostics and remaining work

`status` shows game/running state, reported readiness/players, generation references and pending restarts. `doctor` checks Docker access, SteamCMD path and Wings config; it does not certify retail-game compatibility.

Console logs are `.source/logs/console.log`, rotating at 32 MiB with three older files. Unexpected exits send a bounded redacted console tail and exit code to node-private crash JSON files; local logs remain available without a node.

Real game startup, client authentication/joins, map changes, framework loading and live panel/Wings acceptance remain release gates for all five games. A2S readiness is not proof of third-party plugin health. Scheduled backups, resource metrics, automatic canaries/health rollback, node self-updates and broad maintenance are not implemented in this core phase. Workshop/map caching and the connection guard are excluded.
