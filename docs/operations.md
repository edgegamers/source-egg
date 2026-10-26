# Node operations

These modules implement the applicable S2Script node operations for all five Source games. Enable them in `sudo sourcenode install`, or edit `/etc/sourcenode/config.json` and restart the service. Existing configurations preserve their behavior: scheduled backups, cleanup, metrics, host maintenance and automatic node upgrades are opt-in. Host maintenance starts in dry-run mode. Crash evidence remains enabled; node console archiving is optional.

The changes on `codex/full-feature-parity` require a release containing this implementation. An older installed node/image does not gain these features by changing its config.

## Commands

Server arguments accept a full UUID or an unambiguous prefix. A prefix matching multiple servers is refused. Archived logs/crashes for a removed server can be selected with its full UUID. The node verifies current Docker identity before file operations; containers cannot request privileged operations.

| Command | Action |
|---|---|
| `sudo sourcenode status` | Servers, generations, pending restarts, module counters and next scheduled passes |
| `sudo sourcenode doctor` | Docker/SteamCMD/Wings, disk/reboot state, read-only mounts, webhook GET check, recent crashes and module health |
| `sudo sourcenode backup now [SERVER]` | Back up one selected server, or all running servers |
| `sudo sourcenode backups [SERVER]` | Backup inventory, newest first |
| `sudo sourcenode restore SERVER [INDEX] --yes` | Restore backup 1/newest, or another listed index, onto a stopped server |
| `sudo sourcenode cleanup [SERVER]` | Last and cumulative cleanup results |
| `sudo sourcenode cleanup now [SERVER]` | Apply configured file-age rules now |
| `sudo sourcenode maintenance` | Last host pass and cumulative per-task results |
| `sudo sourcenode maintenance now` | One pass using configured dry-run policy |
| `sudo sourcenode maintenance now --apply` | Explicitly apply one real host pass |
| `sudo sourcenode maintenance setup --yes` | Configure unattended security upgrades with automatic reboot disabled |
| `sudo sourcenode logs [SERVER]` | Node console archive inventory |
| `sudo sourcenode crashes [SERVER]` | Crash bundle inventory |
| `sudo sourcenode crash SERVER [INDEX]` | Sanitized console tail from a bundle |
| `sudo sourcenode metrics` | Current resource and game-state snapshot |
| `sudo sourcenode top` | Interactive resource view: `q` quit, `s` sort, arrows/PageUp/PageDown scroll |
| `sudo sourcenode pins` | Framework fleet policy, server exceptions and installed versions |
| `sudo sourcenode pin metamod VERSION [SERVER]` | Verify an exact release and pin fleet/server framework selection |
| `sudo sourcenode pin sourcemod latest SERVER` | Let this server follow latest despite a fleet pin |
| `sudo sourcenode unpin metamod [SERVER]` | Remove fleet pin, or set a server latest exception |
| `sudo sourcenode unpin all [SERVER]` | Clear all fleet/server pins, or remove one server's exceptions |
| `sudo sourcenode self-update --check` | Check the configured release channel without replacing anything |
| `sudo sourcenode self-update` | Verify/install the node binary and request a service restart |
| `sudo sourcenode update` | Alias for node self-update; `update GAME [BRANCH]` still updates a game |
| `sudo sourcenode uninstall [--purge] --yes` | Remove node service/binary; purge additionally removes only its config |

Without `--yes`, restore, unattended-upgrade setup and uninstall ask in the operator's terminal. Server volumes, shared generations, backups and private node state survive uninstall. A managed server still needs its node/mounts on subsequent starts; removal does not convert it to standalone.

Framework pins and game-generation pins are separate. Existing `pin SERVER GENERATION`, `unpin SERVER`, `history GAME`, `rollback GAME GENERATION` and `unpin-game GAME` remain available. Panel `METAMOD_VERSION`/`SOURCEMOD_VERSION` exact selections take precedence, then node server exceptions, then fleet pins, then the default channel. A pin never silently becomes latest. Selections apply at startup; they do not proactively restart the fleet.

## Backups and restore

Nightly backups cover running servers at the configured node-local hour (default 05:00). The default retains seven archives and fourteen days, with a 1 GiB free-space reserve. Archives live outside server volumes, beneath the node state directory by default. Failed/partial archives are not published.

Private configs, plugins and their data are included. Base VPKs, demos, Steam trees, symlinks, node/Steam credential receipts and byte-identical immutable stock files are excluded. Custom/event content and mutable configs are preserved even if their file size matches stock. `include`/`exclude` patterns are volume-relative paths or globs; `all` includes everything except protected trees and exclusions. Each archive's private metadata records the server and game identity.

Traversal uses bounded directory chunks, a depth limit of 32 and an entry budget, counting excluded entries as work. Backup `max_files` bounds traversal as well as selected files; cleanup/inventory use a 100,000-entry budget. Reaching a bound fails explicitly instead of scanning indefinitely. Privileged JSON receipts must be regular files under their pinned root, without symlinks, and at most 16 MiB.

Restore refuses a running server and rechecks its identity/stopped state during publication. It validates and stages archive members before overlaying individual files. Files absent from the backup remain untouched. Cancellation or I/O failure during the final overlay can leave some files restored and others unchanged; inspect the result or retry while the server stays stopped. Node credentials and game identity receipts are not restored through this mechanism.

## Cleanup and host maintenance

Default cleanup rules remove old demos after seven days, named engine/SourceMod error logs after fourteen days, dumps after seven days and core files after one day. Rules support basename globs, relative directories, recursion, age, enable switches and bounded parent-bundle deletion. The node never follows volume symlinks and can skip files held open by the container. Current configs/plugins/private node receipts and event maps are not blanket cleanup targets.

Host maintenance operates separately from server cleanup. It can remove dangling images, aged Docker build cache and old journal entries. It never invokes `docker image prune -a`, prunes Docker volumes, deletes tagged rollback images or reboots the node. Dry-run passes do not count as freed bytes. Disk-low notices are throttled daily; pending-reboot notices deduplicate until the condition clears. Unattended-upgrade setup is a separate explicit operator command on Debian/Ubuntu.

## Logs, crashes and notices

Node console archives require node `observability.logs.enabled` and server `ENABLE_LOG_FILES=1`. They use UTC daily files, gzip older days and apply configurable retention with a hard 365-day ceiling. Redaction precedes persistence. Queues and daily bytes are bounded; slow/unavailable node delivery drops lines with a console warning instead of blocking the game. Server-local rotated logs remain available independently.

Crash bundles contain a redacted 500-line tail, map/player counts, framework versions and nearby dump/log evidence. Files over 512 MiB, VPKs, Steam trees and symlinks are omitted; total bundle size is bounded. Capture is queued, limited to one per server per minute and retained by configured age/count. Private bundle files may contain native diagnostic evidence; ordinary Discord notices carry metadata, not the native files or credentials. `crash` strips terminal control sequences before display.

Ordinary events are `crash`, `game.update`, `game.update.failed`, `push.failed`, `restart.deferred`, `server.restart`, `node.update`, `backup.done`, `backup.failed`, `cleanup.done`, `maintenance.done`, `disk.low`, `reboot.required`, `server.start`, `server.stop` and `framework.update`. Events are selectable, use configurable Discord identity/embeds, aggregate for three seconds and honor rate limiting with bounded retry/cooldown. Status reports sent/failed/dropped/queued counts. Ordinary delivery is best-effort; generated-password delivery retains its separate private webhook and persisted retry state.

## Metrics and node updates

The Prometheus endpoint defaults to `http://127.0.0.1:9151/metrics`. Explicitly changing it to a public bind requires appropriate network access controls. Metrics use `source_node_*` and `source_server_*` names, with server/game labels: CPU, memory excluding inactive cache, limits, PIDs, network counters, players, map and connected/running state. Unknown player counts remain unknown. Node resource reads and Docker stats do not use RCON. `top` adapts to terminal width and reuses the node samples; without a terminal it prints one snapshot.

Manual self-update supports `--channel stable|beta`, `--release vX.Y.Z` and `--check`. Scheduled self-update is opt-in, follows the installed node channel unless explicitly overridden, verifies release checksums and atomically retains a previous binary. A failed replacement/restart request restores the old binary. The service restart is queued without blocking its own daemon; update notices are emitted by the matching new version after startup. This does not provide runtime-health rollback. Upgrade the server image and egg export separately when a release changes their capabilities.

Password compatibility options are `password_format: "eff"`, `password_words: 3` and `password_calendar_months: 1`. Calendar intervals clamp month-end dates; a positive month interval overrides fixed-hour rotation. Existing assignments and due dates remain valid until their next normal rotation. Token/fixed-hour behavior and per-game default scope remain unchanged unless selected otherwise.

[Configuration reference](reference/node-config.md) · [Server operations](install.md) · [Node installation](node.md)
