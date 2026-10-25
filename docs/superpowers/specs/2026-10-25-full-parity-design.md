# Source node operational parity

The user requested implementation of full feature parity immediately after reviewing `docs/research/2026-10-25-feature-parity.md`. That matrix and reference commit `8857a5519ac4cb56b26716f6d57f7956f60d3a8e` define this work, together with the existing approved Source proposal. Extend the existing Go runtime and five profiles; preserve current installed config and per-game rollout isolation.

## Approach

Extend Source Egg with focused node-side packages and thin broker/control/CLI integrations. Copying the reference node wholesale would reintroduce CS2 assumptions and its single mutable game installation. Running a separate companion would duplicate privileged discovery/configuration. Both conflict with the existing architecture, so implement these functions in the current daemon with optional settings.

## Delivery

- Scheduled private-data backups, filters/exclusions, low-disk refusal, retention, inventory, explicit stopped-server restore with safe archive paths and no deletion of unrelated files.
- Scheduled per-game cleanup for demos/logs/dumps, configurable rules, no symlink traversal, optional open-file protection, parent-bundle safety, persisted statistics and manual commands.
- Opt-in host maintenance: dry-run default, dangling-image pruning only, aged build-cache/journal cleanup; disk/reboot watches; explicit unattended-upgrade setup without automatic reboot.
- Node-hosted bounded daily console archives with gzip/retention and log inventory. Bounded asynchronous delivery must never stall game output.
- Crash bundles: 500-line redacted tail, map/players/versions, nearby dump/log evidence with size bounds, one-per-minute protection, retention and inspection CLI with terminal controls removed.
- Configurable Discord event selection, identity/embeds, three-second aggregation, rate-limit handling, delivery counts and all applicable lifecycle/update/operations notices. Private password messages remain separate.
- Docker/node resource metrics, player/map/game status, loopback Prometheus endpoint and terminal top with sort/scroll/resize/quit.
- Fleet and per-server Metamod/SourceMod pins (server panel exact pin retains highest precedence), inventory and CLI. Existing generation pins remain separate.
- Node update checks, verified atomic binary upgrade, configurable scheduled self-update and uninstall preserving shared/server content; optional config purge. Keep game update commands compatible.
- Richer doctor/status including module, mounts, disk, recent crashes, queue/delivery health and version state.
- Password compatibility options for EFF word passphrases and calendar-month rotation, retaining game/server/node scopes, existing assignments and random-token/fixed-hour settings.
- Wizard, config/reference docs, migration defaults, regression tests and release verification cover all above.

## Defaults and boundaries

New scheduling/export/host-maintenance features are configurable; existing configs keep their behavior. Host maintenance starts disabled and dry-run. Metrics binds loopback by default. New modules are offered by the wizard. No host provisioning, live service restart, backup restore, pruning, release publication or existing server mutation occurs during implementation/tests. Test seams inject host commands, HTTP, clocks and server discovery.

Workshop/custom-map caching and the CS2 connection guard remain excluded. S2Script/MultiAddonManager/GameFixes are not Source 1 dependencies. Automatic canary promotion/health rollback and proactive framework-only restart scheduling are beyond the reference parity benchmark and are not added in this change. Retail-game acceptance remains separate; do not claim live five-game compatibility from CI.

## Safety and acceptance

Use `os.Root` for all server-volume file operations, reject traversal/links and bound archives/network/broker payloads. Discovery and privileged control remain node-owned; a container cannot name another volume, trigger host maintenance, retrieve another password or run arbitrary commands. Restore requires explicit operator intent and a rechecked stopped server. Retention never removes referenced generations, pins, writable event content or active logs.

Tests must demonstrate lifecycle/config preservation, safe paths, failed/partial publication, cancelled work, bounded resource usage and scheduling/retry recovery. Finish with Linux race/vet/format/build checks, bootstrap fixtures, image build and read-only-root startup smoke, plus independent review. Implementation produces a reviewable branch; live acceptance and release publishing require their own concrete actions.
