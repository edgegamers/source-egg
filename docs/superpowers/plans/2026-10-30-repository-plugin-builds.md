# Repository Plugin Builds Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Manage each GitLab/public GitHub SourcePawn repository independently through source builds and the existing plugin lifecycle.

**Architecture:** An optional declarative build recipe resolves immutable source and include snapshots. A private SourceMod toolchain cache and fixed compiler invocation create an ordinary staged package, which the current manager reviews, approves, installs and rolls back.

**Tech Stack:** Go 1.27, standard library, existing addons cache/package extraction and transactional plugin installer.

**Spec:** `docs/superpowers/specs/2026-10-30-repository-plugin-builds.md`

## Global Constraints

- Existing release manifests and exact-package approvals remain compatible.
- No shell, repository scripts, git hooks, automatic approvals or runtime dependency installation.
- Named credentials stay per-server, are restricted to provider origin, and are absent from compiler environment and diagnostics.
- Download/extraction limits remain 256 MiB/512 MiB, 64 MiB per file and 10,000 archive entries.
- Failed builds retain installed files and previous compatible staging; installation occurs only on a normal restart.
- Source recipe counts: 1–100 sources, up to 100 local include directories, up to 20 explicit include dependencies with 1–100 include directories each.
- Work in the existing clean feature checkout; leave changes reviewable and uncommitted, with no external deployment.

## Review Focus

- Dependency-only updates must change source build identity and invalidate neither an already approved snapshot nor unrelated recipes.
- Paths, archive wrappers, output collisions and symlinks must not bypass existing confinement and ownership checks.
- Missing toolchains, compiler failures, cancellation and oversized diagnostics must leave the game and manager usable.
- Old release recipes and persisted receipts must retain their existing hashes and behavior when build/ref fields are absent.
- A changed build recipe must invalidate approval, and compiler tokens must remain private even in failure output.

---

### Task 1: Declarative model and immutable repository acquisition

**Files:** Modify `internal/plugins/model.go`, `internal/plugins/provider.go`; create `internal/plugins/source.go`, `internal/plugins/source_test.go`; extend `internal/plugins/model_test.go`.

**Interfaces:**
- Add `Plugin.Build *Build` (`json:"build,omitempty"`) and `Source.Ref string` (`json:"ref,omitempty"`). `Build` contains `SourceMod string`, `Sources []string`, `IncludeDirs []string`, and `Dependencies []BuildDependency`. A dependency contains `Source Source` and `IncludeDirs []string`.
- Add `Release.Dependencies []Revision`; `Revision` contains the resolved `Commit string` and `Asset Asset`. `HTTPSource.Resolve` dispatches source recipes to source resolution, leaving `ReleaseSource` unchanged.
- `validateBuild(Build) error` and `validateSource(Source) error` centralize recipe checks. Main source refs and dependencies resolve through `HTTPSource.resolveRevision(context.Context, Source) (Revision, error)`.

- [x] Write validation tests for a valid source recipe without release assets, rejected source/release ambiguity, traversal/unsafe refs, invalid glob/target counts and include dependency credentials.
- [x] Run `go test ./internal/plugins -run 'Test.*(SourceRecipe|BuildValidation)' -count=1`; observe rejection of valid new JSON before implementation.
- [x] Implement the model while keeping omitted new fields out of JSON serialization and old recipe hashes unchanged.
- [x] Write HTTP fixture tests for GitLab/GitHub encoded branch paths, exact commit archive URLs, malformed SHAs, credential confinement and dependency-only identity changes.
- [x] Observe new acquisition tests fail, then implement `source.go` with bounded metadata reads and existing `request`/`Download` behavior.
- [x] Run the full plugin package suite; expect all existing release and new acquisition cases to pass.

### Task 2: Private repository compilation and payload mapping

**Files:** Create `internal/plugins/build.go`, `internal/plugins/build_test.go`; refactor `internal/plugins/package.go` only where extraction/mapping reuse needs a seam.

**Interfaces:**
- `SourceBuilder` holds the private cache directory, an optional injected compiler provider for tests, and a `ReleaseSource` downloader. `Build(ctx context.Context, p Plugin, r Release, archive, dest string) (map[string]File, error)` returns a normal mapped staged payload.
- `CompilerProvider` supplies the trusted extracted SourceMod scripting directory for an exact tag. Production uses `addons.Cache`; tests use a fixture compiler binary.
- Shared extraction preserves the current package limits and mapping rules. Source builds remove exactly one archive wrapper and add `compiled` outputs before applying `p.Mappings`.

- [x] Write tests using the Go test executable as a helper compiler: nested source layout, local/dependency includes, renamed/disabled mappings, binary output magic and no leaked provider environment.
- [x] Write failure cases for wrapper ambiguity, archive links/traversal, preexisting compiled directory, duplicate basenames, missing source/include paths, compiler failure, cancellation, bounded diagnostics and invalid outputs.
- [x] Run the focused tests to observe missing builder behavior, then implement fixed compiler invocation and mapping reuse.
- [x] Verify the actual pinned toolchain is obtained only from the framework cache; reject missing expected compiler/include files.
- [x] Run `go test ./internal/plugins ./internal/addons -count=1`; expect all cases to pass.

### Task 3: Manager lifecycle and repository workflow documentation

**Files:** Modify `internal/plugins/manager.go`, `internal/plugins/commands.go`, `docs/reference/plugins.md`, `README.md`; create `internal/plugins/source_manager_test.go` and `docs/examples/plugins-source.json`.

**Interfaces:**
- `Manager.stage` selects the source builder when `Plugin.Build != nil` and otherwise retains release-package preparation. A private builder dependency seam permits deterministic manager tests.
- `Manager.check` reuses an unchanged valid available snapshot and keeps earlier staging on failed builds. Source checks have a five-minute per-plugin budget; source version notices identify revisions.
- `egg plugins` commands stay unchanged; list includes tracked source refs where useful.

- [x] Add a lifecycle test proving source build -> preview -> exact approval -> no hot install -> normal-restart install -> commit update -> rollback; verify failed updates retain both installed and staged bytes.
- [x] Add dependency-only update and build-recipe edit approval tests; observe missing manager dispatch/reuse behavior fail before implementation.
- [x] Integrate the builder without changing release behavior or transaction ownership.
- [x] Document per-repository recipes, dependencies, token scope, source/output roots, compile limits, stock replacement constraints and native-extension limitations; provide a complete parseable example.
- [x] Run `go test ./...`, `go test -race ./internal/plugins ./internal/egg`, `go vet ./...`, and build both commands. Expect successful exits.
- [x] Run a real SourceMod compiler smoke test against a fixture source repository if the pinned public release is reachable; report the difference between fixture coverage and live private-repository validation.
- [x] Obtain a fresh code review, fix material findings, rerun affected checks and leave the final diff reviewable.
