# Source Egg Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans to implement this plan task-by-task. The user approved implementation in this chat; proceed without another approval handoff.

**Goal:** Build a usable Linux Source egg and optional node daemon for five games, sharing immutable base-game VPKs and caching Metamod/SourceMod packages.

**Architecture:** Explicit game profiles drive installation, startup and node discovery. A node owns immutable game generations, framework download caches and scoped join passwords. A per-volume Unix socket binds node requests to one inspected server; Docker/Wings controls lifecycle and read-only mounts.

**Tech Stack:** Go 1.27+, Linux amd64, SteamCMD, Docker/Wings, versioned JSON config, Pelican/Pterodactyl egg exports; Linux build/test through the available Docker Go toolchain.

**Spec:** `docs/source-egg-proposal.md`, approved with latest user changes: no connection guard; password scopes game/server/node; no Workshop or map cache.

## Global constraints

- Games: current standalone CS:GO, CS:S, DOD:S, L4D2, TF2. No game conversion in place.
- Metamod stable and SourceMod prerelease, with exact version pins and private configs/data.
- Immutable base-game generations; running processes never observe a moving shared target.
- No Workshop acquisition, custom-map cache, event installer, or connection/network guard.
- Generated join passwords opt in per server; node default scope `game`, alternatives `server` and `node`; sharing never crosses node boundaries. Manual join password and RCON remain separate.
- Do not modify unrelated connected game servers. Real five-game client acceptance remains an explicit external validation requirement.

## Review focus

- Server-planted symlinks must not redirect privileged cache/deployment writes outside a volume.
- A shared image, stale handshake or changing profile must not cause an unrelated game update/restart.
- Partial Steam/framework downloads must not publish a generation or corrupt prior working installs.
- Generation rollout/cleanup must retain active references and uploaded custom maps.
- Password lookup/rotation must never let a server select another server's scope or disclose credentials in ordinary logs.

## Tasks

### 1. Profiles, install identity, and launch contract

Files: `internal/game/{profile,launch}.go`, tests; `go.mod`.
Interfaces: `game.Lookup(string) (Profile,error)`, `Profile.Identity(branch string) Identity`, `Profile.Command(Launch) ([]string,error)`.

- [x] Write/run failing tests for five install/startup recipes, standalone CS:GO identity, rejected selector/branch/config injection and profile changes.
- [x] Implement profile registry, safe argv construction and persistent install identity.
- [x] Run `go test ./internal/game` to green.

### 2. Scoped password store

Files: `internal/passwords/{store,store_test}.go`.
Interfaces: `Store.Get(scope, server, game string, now time.Time) (Assignment,error)`, `Store.Rotate(...)`; authoritative server identity comes from the node.

- [x] Write/run failing tests for same-game sharing, different games, server/node alternatives, persistence, invalid scope and rotation.
- [x] Implement crypto-random join passwords, atomic private state, default game scope, configured rotation interval, explicit reveal command.
- [x] Run `go test ./internal/passwords` to green.

### 3. Framework resolver/cache/deployer

Files: `internal/addons/{releases,cache,deploy}.go`, integration tests.
Interfaces: resolver selects official release/channel/Linux asset; cache returns verified immutable archive; deploy preserves server-owned state and records managed package files.

- [x] Write/run failing tests using HTTP archive fixtures for stable vs prerelease, missing pins, concurrent downloads, corrupt/traversing archives and preserved custom configs/plugins.
- [x] Implement official-origin resolution, bounded archive extraction, atomic cache publication and owned-file deployment.
- [x] Run `go test ./internal/addons` to green.

### 4. Steam installs and immutable base-game generations

Files: `internal/content/{steam,store,deploy}.go`, filesystem/integration tests.
Interfaces: `Installer.Install(ctx, identity, dest)`, `Store.Ensure(...) (Generation,error)`, `Deploy(generation, volume, mountedRoot)`; durable generation references.

- [x] Write/run failing tests with fake Steam executable and real files for app/branch selection, partial install rejection, pinned old generations, VPK-only sharing, preserved event maps, and symlink escape refusal.
- [x] Implement complete staged install validation, generation publication, private non-VPK delivery, receipts and explicit safe pruning.
- [x] Run `go test ./internal/content` to green.

### 5. Docker/Wings adapters, protocol, and targeted coordinator

Files: `internal/node/{docker,config,daemon,protocol,rollout}.go`, tests; adapted Linux mount/Wings helpers with attribution.

- [x] Write/run failing tests for inspected game identity, per-volume authenticated requests, CS:S-only rollout in a mixed fleet, stopped servers, stale identity, unknown player count, Wings errors and credential isolation.
- [ ] Implement local Docker discovery, bounded per-server sockets, host read-only mounts, managed boot preparation, framework cache delivery, scheduled scoped Wings restarts, passwords, webhook events, status and doctor.
- [x] Run `go test ./internal/node` to green.

Core coordination, mount/socket isolation, caching, passwords, webhooks, status/doctor and manual rollback/pins are implemented. Automatic canary promotion/verified health rollback remain release work, so the full implementation step above stays open.

### 6. Container boot and supervisor

Files: `internal/egg/*.go`, `cmd/sourceegg/main.go`, `cmd/sourcenode/main.go`, tests; adapted PTY supervisor and notices.

- [x] Write/run failing integration tests for standalone boot, managed boot refusing local Steam writes, config preservation, private passwords, graceful stop, crash reporting and redacted console output.
- [x] Wire Steam/framework preparation, node communication, safe launch arguments, console forwarding, durable logs, restart requests and generation reporting.
- [x] Run `go test ./...` to green.

### 7. Packaging, setup, docs, and CI

Files: `Dockerfile`, `pterodactyl/*.json`, `scripts/*`, `configs/*`, `.github/workflows/*`, README/install docs.

- [ ] Generate/import-validate game/password enumerations and runtime settings; add reproducible container/release builds and node systemd setup.
- [x] Document installation, scope examples, direct password reveal, pins, rollback, shared content, and pending live-game acceptance.
- [ ] Run Linux tests/race/vet/build and container boot smoke checks, then request independent code review and address material findings.

## Execution record

Implementation branch: `codex/source-egg-implementation`. Work in the current project checkout to preserve the uncommitted approved planning documents; no additional worktree is needed for this initially empty repository. Use the existing Docker engine for Linux Go verification. Do not commit or publish automatically; leave the result reviewable in the workspace.

Core review completed by a read-only independent reviewer. Seven material findings were reproduced/addressed: container-root mount targeting, service umask, writable SteamCMD under Wings, stock SourceMod upgrades, deferred-rollout persistence, prune/boot synchronization and strict maintenance windows. Regression names include `TestReadOnlyMountTargetsContainerRoot`, `TestSteamClientSeedIsWritableAndPreservesSessions`, `TestUpgradeUpdatesStockPluginsAndExtensionsButPreservesCustomEdits`, `TestDaemonRestartReconcilesOldServersAndPreservesDeadline`, `TestPruneWaitsForBootGenerationReservation`, and `TestWindowRequiresWindowEvenWhenEmpty`. The read-only-root/UID-988 image smoke covers runtime permissions. Added a single-daemon state lock and console-injection/credential-redaction regressions during hardening.

Observed verification: Linux unit/integration suite with race detector, `go vet`, formatting, Linux binary builds and shell syntax checks passed through `scripts/verify.sh`. Real upstream MM/SM archive resolution/cache/deployment passed. Isolated mount-namespace read-only proof passed. Docker dev image build and Wings-like smoke passed. Actual SteamCMD public build metadata checks passed for all five games (without retail installation). Final core verification is rerun after the last hardening edits; live game/client and panel acceptance plus remaining operational features stay open in IMPLEMENTATION.md.

Final core verification after all hardening edits: `scripts/verify.sh` passed, `docker build -t source-egg:dev .` passed, and `scripts/smoke-container.sh source-egg:dev` passed. Built binaries are in ignored `dist/`; the local Docker image is `source-egg:dev`. No commit, push, remote release or live-server change was performed.

Integration follow-up: the user explicitly authorized merging into `main` and pushing with the ShookEagle account, superseding the earlier leave-uncommitted instruction for the implementation run. Active GitHub authentication was verified as ShookEagle and `scripts/verify.sh` passed again before integration. The square Source icon is embedded in the egg export; runtime acceptance and remaining release features remain tracked in IMPLEMENTATION.md.
