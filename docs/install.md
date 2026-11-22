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

Frameworks unpack into private temporary directories on the server volume, following the S2Script egg's disk-backed staging approach. Staging is removed after success or failure. Wings' default 100 MB `/tmp` limit is supported without changing the node configuration; the server volume needs room for both staging and the installed files.

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

The wizard reviews settings, writes `/etc/sourcenode/config.json` and enables/restarts the service. Advanced edits can be made directly, followed by a service restart. Relevant fresh-install defaults:

```json
{
  "node_channel": "stable",
  "prefetch_games": [],
  "shared": true,
  "push_method": "symlink",
  "max_workers": 8,
  "auto_restart": true,
  "steam_validate": false,
  "framework_cache": true,
  "poll_seconds": 5,
  "check_minutes": 1,
  "restart": {
    "mode": "immediate",
    "window": "04:00-06:00",
    "max_delay_minutes": 720,
    "warn_seconds": 300
  },
  "password_enabled": false,
  "password_scope": "server",
  "password_format": "eff",
  "password_calendar_months": 1,
  "password_interval_hours": 0,
  "password_timezone": "UTC",
  "webhook": "",
  "password_webhook": ""
}
```

The excerpt omits path/image/Steam settings and operational modules. Existing configurations retain their saved settings and legacy defaults. Set `wings_config` for nonstandard config locations; otherwise the daemon checks `/etc/pelican/config.yml` and `/etc/pterodactyl/config.yml`. HTTPS restarts pin the certificate configured in Wings.

Restart policies are `immediate`, `empty`, and `window`. Window mode restarts when empty or within `HH:MM-HH:MM` in the node's local timezone. A nonzero maximum delay can override waiting; zero disables that deadline. Unknown player counts are never treated as empty. Local A2S counts exclude bots. Disable `auto_restart` to stage updates until a manual server restart.

`SOURCE_MODE=standalone` keeps private game files while still allowing node caching/passwords. `managed` requires sharing; `auto` chooses on first start. An already managed volume refuses unsafe local SteamCMD fallback. Materializing it back to standalone is not automated.

## Shared content and rollback

Complete builds live under `/srv/source-shared/generations/<game>/<branch>/<runtime>/<build-token>`. Updates use private staging copies, validate the install and publish fresh generations. Peak disk use includes retained builds plus staging; unchanged remote build IDs avoid staging copies.

Running servers keep their concrete VPK targets until restart. Server configs, addons, logs, gameinfo and event content remain private. Event maps are ordinary uploads; no Workshop acquisition or map cache is provided.

Symlink delivery remains the default. `hardlink` shares VPK inodes on the same filesystem, requiring a stable non-root container UID distinct from the shared file owner. `copy` makes private VPK copies. Unsupported hardlinks fail explicitly, preserving existing files. Both alternate modes require a matched egg image supporting node-side delivery; old images continue to work with symlinks. Immutable generations remain the update/rollback boundary in all modes.

Repeated starts reuse unchanged private copies of stock maps, binaries and other loose game files from the same generation. The deployment receipt records source and destination file identity, size, mode and modification time; missing, edited or replaced stock files are copied again. The check deliberately ignores `ctime`, which Wings changes during its ownership pass even when file contents are unchanged. This is a metadata quick check rather than a full content hash. A different generation performs a fresh delivery. Volumes without copy metadata need one complete delivery to record it, then later starts avoid the repeated file copies and flushes; existing copy receipts remain compatible. If optional metadata would exceed the receipt size limit, full delivery remains available. This optimization runs in `sourcenode` for host delivery, so updating only the game container image does not update the node's delivery code.

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

Framework downloads are cached centrally and applied at startup. Missing exact framework pins fail. Node fleet/server pins and inventory are available through `pins`, `pin metamod VERSION [SERVER]` and equivalent SourceMod commands. Configured archive retention protects installed/pinned versions; unknown legacy archives remain conservative until requested again. Proactive framework-only restart scheduling is outside the reference-parity scope.

## Passwords

Generated passwords apply to `sv_password`, not RCON. Enable node password management in the wizard and `GENERATE_PASSWORD=1` on participating dev servers. Fresh nodes offer `server` scope; existing nodes retain their saved scope (legacy default `game`).

| Scope | Sharing boundary |
| --- | --- |
| `game` | Same game on the same node, among participating servers |
| `server` | One server UUID |
| `node` | Participating servers across games on this node |

Admin-only `PASSWORD_SCOPE=default` follows node policy; `game`, `server` and `node` override it per server. No sharing crosses nodes. Assignments persist privately. Choose EFF word phrases (three words by default, optional custom dictionary) or tokens containing 18 cryptographically random bytes encoded as 24 URL-safe characters. Fresh opt-in uses monthly rotation; legacy token/hour settings remain unchanged on upgrades.

The wizard offers daily, weekly, bi-weekly, monthly, custom hours/months, never, and cron. For Monday 04:00 Chicago rotation, use `password_cron: "0 4 * * MON"` and `password_timezone: "America/Chicago"`. Existing assignments keep their saved due dates; new assignments and later rotations use the selected schedule. See [cron syntax, DST and migration details](reference/node-config.md#password-schedules).

The first query after expiry regenerates the scope assignment. Each running participant detects the change and requests its own Wings restart; stopped servers apply it on next startup. Password rotation is separate from game-update windows. Generated-password startup fails closed without a node assignment.

```bash
sudo sourcenode passwords show FULL_SERVER_UUID
```

Ordinary `webhook` events exclude credentials. Optional `password_webhook` sends actual join passwords to a separately configured private Discord channel and retries unconfirmed deliveries on subsequent assignment checks. Root can reveal credentials explicitly. `RCON_PASSWORD` remains independent.

Disabling generation makes `SERVER_PASSWORD` authoritative; an empty manual value means an open server. Live console overrides remain until startup reapplies configured values and are redacted from saved logs.

The node's separate master switch stops new assignments/rotation while retaining cached enrolled-server passwords. To return an individual server to its manual password, turn off its panel `GENERATE_PASSWORD` switch.

## Diagnostics and remaining work

`status` shows game/running state, reported readiness/players, generation references, pending restarts, module counters and schedules. `doctor` checks Docker, SteamCMD, Wings, disk/reboot state, mounted paths, webhook availability and recent crashes; it does not certify retail-game compatibility.

Local console logs remain `.source/logs/console.log`, rotating at 32 MiB with three older files. Optional node archives add daily gzip/retention; server `ENABLE_LOG_FILES=1` enables forwarding. Unexpected exits queue private crash bundles with a 500-line tail, map/players, framework versions and nearby evidence. Bounded queues protect console throughput.

Backups/restore, resource metrics/Prometheus/top, scheduled cleanup, host maintenance, verified node updates and uninstall are documented in [Node operations](operations.md). Panel startup parameters, SourceMod plugin switches, standalone cleanup, console filtering and debugging are in [Server controls](server-controls.md). See the [complete node configuration](reference/node-config.md).

Real game startup, client authentication/joins, map changes, framework loading and live panel/Wings acceptance remain release gates for all five games. A2S readiness is not proof of third-party plugin health. Automatic canaries/health rollback remain beyond reference parity. Workshop/map caching and the connection guard are excluded.
