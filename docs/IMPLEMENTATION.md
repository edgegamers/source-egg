# Implementation status

The core implementation was merged into `main`. The reference-style installation follow-up is on `codex/reference-install-flow`. This records delivered behavior separately from the full-release proposal.

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

## Verification

Linux tests cover profile changes, Steam arguments, failed install publication, immutable generations, retained event maps, traversal/symlink rejection, cache coalescing, framework upgrades, password scopes/persistence, targeted rollout, restart recovery, pruning/boot synchronization, windows, generation pins, A2S challenges, PTY shutdown and log bounds.

Real upstream Metamod 1.12.0.1228 and SourceMod 1.13.0.7478 Linux archives were fetched, validated, cached and deployed in isolated tests. The mount regression was reproduced then verified in an isolated namespace with a distinct filesystem root; reads reach the container target and writes are denied. Final verification results are recorded in the execution plan.

The built image also passed fixture boot with a read-only root, UID 988, writable SteamCMD seed and PTY shutdown. Public Steam build metadata resolved for all five games through the actual SteamCMD client; this is not a retail-game install/client-join test.

## Remaining release work

1. Live Linux Wings proof for all five retail installs, real client joins/authentication, map changes, actual framework loading and game-only rollouts. Current standalone CS:GO remains the highest-risk case.
2. Real panel import/startup acceptance and a dev-node rollout of the published image/binary pair.
3. Canary promotion and verified health rollback. Framework updates currently apply at startup; proactive framework-only restart scheduling is not implemented.
4. Scheduled backups, resource metrics, broader retention/cleanup, node self-updates and optional host maintenance. The proposal's initial-release target list does not imply these already exist.

Workshop acquisition, custom-map caching and the connection guard are excluded. No connected live game server has been changed; no release or image has been pushed.

## Execution decisions

- Preserve approved uncommitted plans in the current checkout on a new implementation branch.
- Use Go 1.27 and the reference's tested PTY/Linux dependency versions, verified in Docker.
- Require Linux 5.12+ so read-only attributes are applied before shared mounts become visible.
- Honor strict maintenance windows; a configured maximum delay can override them.
- Use explicit 0700/0600 private state permissions and service umask 0022 for public mounted content/socket directories.
- Retain orphan references and old framework archives conservatively until cleanup policy is implemented, favoring recovery over automatic disk reclamation.
