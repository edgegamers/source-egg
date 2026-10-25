# Full Reference Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Independent package tasks use the parallel-agent skill; integration stays sequential.

**Goal:** Implement the reference node's applicable operational features for all five Source games.

**Architecture:** Optional Go packages own observability, file/host operations and node lifecycle. The node owns discovery, config, scheduling, privileged CLI and bounded broker translation; game processes remain unprivileged. Parent owns integration/shared files to prevent concurrent edits.

**Tech Stack:** Go 1.27, Linux os.Root/x/sys, Docker HTTP API, unix HTTP broker, systemd, existing Steam/framework stores, Docker-based verification.

**Spec:** `docs/superpowers/specs/2026-10-25-full-parity-design.md`; reference `.tools/reference` at `8857a551`; matrix `docs/research/2026-10-25-feature-parity.md`.

## Global Constraints

- Preserve game/app/branch/runtime identity targeting, server-owned config and game/server/node password scopes.
- No Workshop/map cache, connection guard, Source 2 addon providers, automatic canary/health rollback or proactive framework-only restart scheduler.
- Go 1.27; no new container/host requirement for Go/Python during release install.
- New host actions optional and dry-run by default. Tests never change a live host/server.
- All cross-volume operations rooted; only operator control can restore/update/prune/configure host.
- Parent owns `internal/node/*`, `internal/egg/*`, CLI, wizard, workflows and docs; agents own only their assigned independent packages and tests, reporting APIs before integration. No worker commits concurrent shared state.

## Review Focus

1. A volume symlink/archive member reaching outside root must fail without modifying its target (operations regression).
2. Busy/disconnected/slow servers or Discord must not block console output or unboundedly grow queues (observability regression).
3. Restarted daemon and expired timers must not repeat destructive scheduled work or reset retained credentials (operations/lifecycle regression).
4. Container IDs/prefix ambiguity and game changes must never select a different server for restore/cleanup/pin (integration regression).
5. Uninstall or upgrade failure must preserve running games, config/previous binary and private state (lifecycle regression).

### Task 1: File and host operations

Files: create `internal/operations/*.go` and tests. Read reference backup/cleanup/maintenance source and docs.
Produces: package-local Config/DefaultConfig/Validate; Manager with backups/list/restore, cleanup/list, maintenance/status/setup, scheduling and injectable server/command/notice callbacks. Use standalone server metadata, not node imports, to avoid cycles. Report exact exported API to parent before integration.

- [ ] Write failing tests for backup exclusions/custom-event preservation, low disk, safe/stopped restore, symlink/tar rejection; implement bounded atomic tar.gz archives and retention.
- [ ] Write failing cleanup tests for age/globs, open files, symlinks, delete-parent bounds and persisted statistics; implement scheduled/manual cleanup.
- [ ] Write failing maintenance tests for dry-run, safe command arguments, disk/reboot dedup and recovery; implement injectable commands/setup and schedule.
- [ ] Run Linux `go test -race ./internal/operations/...`; report red/green evidence, exact APIs and limitations. Parent reviews task diff.

### Task 2: Observability

Files: create `internal/observability/*.go` and tests. Read reference alerts/consolelog/crashes docs/source.
Produces: Config/DefaultConfig/Validate; Manager for events, counters, context-run aggregation, bounded async log ingestion, daily archive listing/sweep, bounded crash bundles/list/tail. Inputs are server UUID/volume/game/name/map/players/versions; no node imports. Report exact APIs.

- [ ] Write failing tests for event filters/3s aggregation/429/retry/counters/identity/secret-safe output; implement nonblocking manager.
- [ ] Write failing tests for bounded log delivery, UTC daily archives/gzip/hard365-day retention/path safety; implement archive storage and inventory.
- [ ] Write failing tests for 500-line crash tail, nearby file/size exclusions, per-minute rate limit/retention, sanitized inspection and cancellation; implement bundle storage.
- [ ] Run Linux `go test -race ./internal/observability/...`; report APIs/red-green evidence. Parent reviews task diff.

### Task 3: Lifecycle and password compatibility

Files: create `internal/lifecycle/*.go`, `internal/nodeinstall/uninstall.go` and tests; extend only `internal/passwords/*` for optional formats/schedule. Do not edit existing installer/wizard/shared node files.
Produces: testable updater/check/install and root-scoped safe uninstall using injected host commands/paths; optional password Open options retaining existing Open API and assignments.

- [ ] Write failing tests for stable/beta/exact selection, wrong/missing checksums, atomic replace, config preservation, update schedule and rollback on command failure; implement verified update/check operations.
- [ ] Write failing tests for uninstall preserving server/shared/state data and optional config-only purge, failure reporting; implement uninstall with fixed/injected service/binary paths.
- [ ] Write failing password tests for EFF words/calendar month rollover/end-of-month and existing assignment preservation; implement optional formats without changing default scopes or exposing secrets.
- [ ] Run Linux race tests for lifecycle/nodeinstall/passwords; report exported APIs and red-green evidence. Parent reviews task diff.

### Task 4: Metrics and framework policy

Files: create `internal/metrics/*`; node Docker/stats integration; `internal/node/frameworks.go`; tests.
Consumes Docker GET seam and local A2S/map state. Produces CPU delta/cache-excluded memory/network/host samples, escaped Prometheus metrics and interactive top. Fleet/server/panel framework policy and inventory remain separate from game generations.

- [ ] Write failing metrics tests for CPU denominator/idle resets, cache memory subtraction, counter reset, unavailable state and malicious label escaping; implement read-only sampling/export/top.
- [ ] Write failing policy tests for panel/server/fleet precedence, explicit unpin and persistence/missing exact release; implement privileged pins inventory/CLI.
- [ ] Verify scoped Linux tests/race; commit independent work only once assigned agents report no concurrent staging.

### Task 5: Daemon, broker, CLI and wizard integration

Files: node config/daemon/control/protocol/docker/alerts/rollout; egg config/run/query; sourcenode CLI; wizard/install. Consume the exact package APIs produced in Tasks 1–4; record reconciled contracts in ledger before connecting.

- [ ] Add default-preserving typed module configs and wizard toggles; tests cover existing JSON/headless installs and invalid listen/path/rule settings.
- [ ] Add context-owned managers/schedulers with shutdown, status/doctor and protected control endpoints. Add unique server-prefix resolution and runtime identity rechecks for dangerous commands.
- [ ] Add bounded redacted log/crash/version/map delivery, lifecycle/deferred/push/update/operations events and failure evidence without blocking PTY output. Keep old broker requests compatible within the version.
- [ ] Add CLI backups/backup/restore/cleanup/maintenance/logs/crashes/crash/metrics/top/frameworks pins/self-update/check/uninstall; maintain existing game update/pin commands. Restore and uninstall are explicit operator actions; no live execution in tests.
- [ ] Run all Linux race tests plus installer/seed fixtures and integrated end-to-end unix HTTP tests with fake Docker/Wings.

### Task 6: Docs, verification and review

- [ ] Update README/node/server/development guides, complete config/commands reference and delivered parity matrix; correct stale branch/release text.
- [ ] Verify Linux race/vet/gofmt/build/shell/fixtures, image build, profiles and read-only-root smoke; preserve explicit retail acceptance boundary.
- [ ] Independent task/whole-branch review; fix material findings with reproductions and retain evidence in ledger.
- [ ] Commit reviewable changes on `codex/full-feature-parity`; do not run live node provisioning, publish a release or merge without a concrete integration instruction.
