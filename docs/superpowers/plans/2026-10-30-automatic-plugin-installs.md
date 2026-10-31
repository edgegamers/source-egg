# Automatic Plugin Installs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Install opted-in ready plugin versions on normal startup without routine package approvals.

**Architecture:** Add an inherited boolean approval policy and reuse installer preview/pending transactions. Queue cached/checked snapshots before one startup apply pass so ready builds install on that launch, while explicit pending actions retain priority.

**Tech Stack:** Go 1.27, existing plugin manager/installer, JSON manifests.

**Spec:** `docs/superpowers/specs/2026-10-30-automatic-plugin-installs-design.md`.

## Global Constraints

- Manifest `auto_approve` defaults to false; per-plugin optional boolean overrides it.
- Policy never changes recipe hashes or staged package identities.
- Preserve the 30-second startup check budget; install only with the game stopped.
- Automatic queueing always uses `replace=false` and existing ownership/edit checks.
- Explicit pending install/rollback/uninstall actions take precedence for that startup.
- Preserve failed-build fallback, configuration/data/logs, pins and recovery.

## Review Focus

- Already staged packages must install after enabling auto even with the provider offline.
- A manual rollback/uninstall must not be undone during the same startup.
- Turning automatic approval off must cancel persisted automatic work across recreation.
- Cached source builds and failed newer builds must retain correct automatic snapshots.
- A stale queued revision must not override a newly resolved already-installed revision.

### Task 1: Automatic approval policy and lifecycle

**Files:**
- Modify `internal/plugins/model.go`, `state.go`, `manager.go`, `commands.go`.
- Create `internal/plugins/automatic.go`, `automatic_test.go`.
- Modify `README.md`, `docs/reference/plugins.md`, `docs/examples/plugins-source.json`.
- Update ignored `dist/css-server-config/plugins*.json` and `SETUP.txt`.

**Interfaces:**
- Manifest `AutoApprove bool`; Plugin `AutoApprove *bool`; pendingAction `Automatic bool`, all JSON `auto_approve`/`automatic` with omission where appropriate.
- `(*Manager).autoApprove(Plugin) bool` resolves overrides.
- `(*Manager).queueAutomatic(Plugin, Snapshot)` previews and queues without replacing explicit actions.
- `(*Manager).applyPending() error` applies the existing action loop once after startup discovery.
- `recipeHash(Plugin)` excludes approval policy.

- [x] Add behavior regressions for all validation/review-focus cases; run and observe failures.
- [x] Add policy fields, hash exclusion, effective list output and automatic queue/cancellation logic.
- [x] Refactor startup discovery/apply ordering; keep explicit actions protected and remove installed matching availability.
- [x] Run focused regressions and the existing full plugin/egg lifecycle suite; resolve failures.
- [x] Document automatic mode, pins, conflicts, explicit action behavior and restart timing; opt this server into automatic tracking.
- [x] Run `go test -race ./...`, `go vet ./...`, `CGO_ENABLED=0 go build ./cmd/...`, formatting and manifest validation.
- [x] Commit the feature and docs; record the completion contract in the execution ledger.
- [ ] Obtain a fresh whole-branch review, fix actionable findings with RED→GREEN tests, push and open a PR.
