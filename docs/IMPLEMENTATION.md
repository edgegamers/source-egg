# Implementation status

The core, installation flow and operational modules are merged through `7cc3e68`. The upstream-fork parity update builds on that baseline using S2Script commit `8857a5519ac4cb56b26716f6d57f7956f60d3a8e`. This records implemented behavior separately from live acceptance and the proposal's additional goals.

## Implemented

- Five game profiles and validated launch/Steam install identities, including current standalone CS:GO app mapping and mixed 32/64-bit dependencies.
- Standalone/node-managed boot, including a writable SteamCMD seed for Wings' read-only root and arbitrary UID.
- Immutable staged base-game generations, read-only VPK delivery, private configs/event files and persistent references.
- Exact approved-image discovery, profile/boot checks, bounded per-server sockets and root-only operator control.
- Game-specific update checks, persisted deferred restarts and immediate/empty/window scheduling through Wings.
- Persistent per-server generation pins, game-wide manual rollback/pins and protected generation pruning.
- Metamod stable/SourceMod prerelease resolution, exact pins, coalesced metadata/archive cache and private deployment.
- Hash-aware stock plugin/extension upgrades preserving custom edits/configs and disabled/deleted defaults.
- Opt-in generated join passwords scoped to game (default dev), server or node; persistence, rotation, root reveal and optional separate credential webhook.
- Ordinary Discord events, masked/rotating console logs, local A2S state and saved crash reports.
- Egg export, Dockerfile, local/release node installer, CI/release workflows and usage docs.
- Reference-style sudo/TTY-aware bootstrap and interactive `sourcenode install` wizard, saved-setting preservation, headless setup and optional selected-game prefetch before servers exist.
- Nightly private-data backups, configurable filters/retention/free-space reserve, inventories and explicit stopped-server restore with game/identity rechecks.
- Scheduled server demo/log/dump cleanup, open-file protection, safe parent-bundle rules and persisted cumulative per-server/per-rule statistics.
- Optional dry-run host maintenance, dangling-image/aged build-cache/journal cleanup, disk/reboot notices and explicit unattended-upgrade setup without automatic reboot.
- Configurable aggregated Discord embeds/event filters/identity, bounded delivery queues/retries and delivery counters; separate private password delivery remains.
- Bounded nonblocking node console forwarding, UTC daily/gzip archives, retention and log inventory; richer crash bundles/inspection with 500-line tails and evidence bounds.
- Docker/node CPU/memory/network/PID metrics, loopback Prometheus and interactive terminal top with responsive columns/sorting/scrolling.
- Fleet/server framework pins and inventory, panel precedence, configurable metadata refresh and protected archive retention.
- Verified atomic node upgrade/check, optional scheduled self-update, previous-binary rollback on failed update requests and safe uninstall retaining server/shared/private data.
- Optional EFF-word passwords/calendar-month rotation preserving existing assignments, scopes and fixed-hour/token behavior.
- Extended wizard, module status/schedules/doctor and complete operator/configuration guides.
- Upstream-style terminal sections, help, choices, settings review, progress and command reference, with embedded EdgeGamers and Source 1 artwork and plain/narrow fallbacks.
- Wizard controls for framework cache/pins, backup filters and unlimited age/count, cleanup per-rule ages, maintenance tasks/retention, Discord events/identity, metrics sampling and all password policies.
- Five-field cron password rotation with IANA zones, DST handling and single overdue catch-up; custom dictionaries, private state path and master enable control. Saved passwords and due dates remain intact.
- Readable CLI tables and summaries, global `--json` preserving API responses, safe terminal output and consistent server-prefix lookup.
- Symlink/hardlink/copy VPK delivery, bounded preparation concurrency, node Steam validation, optional automatic game restarts and empty-or-window restart semantics.
- Standalone startup cleanup, editable console filters/levels, quoted custom startup arguments, opt-in gdbserver and SourceMod stock plugin switches preserving custom files.
- Fresh-install defaults aligned with the applicable upstream modules, separated from legacy loading defaults so upgrades preserve saved behavior.

## Verification

Linux tests cover profile changes, Steam arguments, failed install publication, immutable generations, retained event maps, traversal/symlink rejection, cache coalescing, framework upgrades, password scopes/persistence, targeted rollout, restart recovery, pruning/boot synchronization, windows, generation pins, A2S challenges, PTY shutdown and log bounds.

Real upstream Metamod 1.12.0.1228 and SourceMod 1.13.0.7478 Linux archives were fetched, validated, cached and deployed in isolated tests. The mount regression was reproduced then verified in an isolated namespace with a distinct filesystem root; reads reach the container target and writes are denied. Final verification results are recorded in the execution plan.

The earlier baseline image passed fixture boot with a read-only root, UID 988, writable SteamCMD seed and PTY shutdown. Public Steam build metadata resolved for all five games through the actual SteamCMD client; this is not a retail-game install/client-join test. Those earlier results do not certify the updated image's new debugging/startup features.

Operational tests cover rooted backups/restores/cleanup, live-state rechecks through a private control socket, transient schedule-state failures, container open-file paths, real prune output accounting, bounded log/crash queues, gzip rollover/orphans, cancellable traversal, credential/terminal sanitization, Prometheus escaping/counters, policy precedence, verified binary swaps/rollback/uninstall and calendar/password persistence. Component reviews and fixes are recorded in the parity execution ledger; final integrated verification is recorded in its plan.

The upstream-fork update passed the complete verification script: Go race tests, vet, formatting, both Linux builds, shell syntax and SteamCMD seed fixtures. Bootstrap fixtures passed separately. An independent review reproduced and rechecked fixes for private password path aliases, disabled-policy cached startup, obsolete relocated stock plugins and the panel readiness marker. The actual wizard transcript was rendered and visually inspected. The updated image smoke and live game/debugger acceptance were not run because Docker access is unavailable on this host. See the [execution ledger](superpowers/plans/2026-10-26-upstream-fork-parity.md).

## Remaining release work

1. Live Linux Wings proof for all five retail installs, real client joins/authentication, map changes, actual framework loading and game-only rollouts. Current standalone CS:GO remains the highest-risk case.
2. Real panel import/startup acceptance and a dev-node rollout of the published image/binary pair.
3. Publish and roll out a matched node/image/egg release containing operational parity, then perform live module acceptance on a dev node.
4. Additional proposal goals beyond the reference benchmark: canary promotion, verified runtime-health rollback and proactive framework-only restart scheduling. Framework updates currently apply at startup.

Workshop acquisition, custom-map caching and the CS2 connection guard are excluded. No connected live game server was changed by this implementation. Earlier releases exist; this update has not published a new node/image/egg release.

## Execution decisions

- Keep implementation isolated in an attached worktree, preserving the original checkout.
- Use Go 1.27 and the reference's PTY/Linux dependency versions. This update uses a checksum-verified native Go toolchain; Docker access is unavailable on the development host.
- Require Linux 5.12+ so read-only attributes are applied before shared mounts become visible.
- Match upstream empty-or-window restarts; unknown player counts wait for a window/deadline instead of being assumed empty.
- Use explicit 0700/0600 private state permissions and service umask 0022 for public mounted content/socket directories.
- Retain orphan generation references conservatively; framework retention protects installed/pinned/unknown legacy archives.
