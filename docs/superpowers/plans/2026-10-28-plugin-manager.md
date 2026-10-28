# Per-server Plugin Manager Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Deliver the approved owner-managed plugin updater and a new PR.

**Architecture:** A separate internal/plugins package owns recipe validation, release acquisition, immutable staged packages and transactional deployment. A manager coordinates persisted approvals and console commands; egg lifecycle hooks run startup deployment and asynchronous checks without involving the node.

**Tech Stack:** Go 1.27 standard library, existing PTY supervisor and fsx helpers. No new runtime dependency.

**Spec:** docs/superpowers/specs/2026-10-28-plugin-manager.md

## Global Constraints

- Per-server editable files; public GitHub and private GitLab; no node access required.
- Check startup and every six hours by default; approval of exact staged content; install at next restart.
- Preserve configuration/data/logs, protect file ownership and local modifications; retain two previous versions.
- Bounded network/archive inputs; credentials remain on their configured GitLab origin; no scripts.
- TDD for meaningful behavior, full race/build/vet/bootstrap verification and independent final review.

## Review Focus

- Crash between file writes and receipt commit must recover the old consistent installation.
- Manifest edits or a changed remote asset must never silently change an existing approval.
- Framework-owned files, symlink parents and locally edited files must not be overwritten.
- Chunked panel input must consume reserved commands without forwarding fragments to srcds or blocking stop.
- Redirects and provider failures must not leak tokens or prevent an otherwise viable server boot.

## Shared interfaces

`Manifest{Version int, CheckIntervalHours int, Plugins []Plugin}`; `Plugin{ID string, Source Source, Asset string, Format string, Mappings []Mapping, Preserve []string, Pin string, Prerelease bool}`; `Source{Provider,Repository,URL,Credential string}`; `Mapping{From,To string}`.
`Asset{Name,URL,SHA256 string}`; `Release{Version,ID string,Asset Asset}`; `File{SHA256 string,Seed bool}`; `Snapshot{ID,Version,Recipe,Directory string,Files map[string]File}` where Directory is volume-relative `.source/plugins/packages/<id>` and Files are game-relative.
`ReleaseSource` interface: `Resolve(context.Context,Plugin)(Release,error)` and `Download(context.Context,Plugin,Release,string)error` (last string destination archive). `HTTPSource{Client *http.Client, Credentials map[string]string}` implements it. `PreparePackage(archive,dest string,p Plugin,assetName string)(map[string]File,error)` unpacks/maps into dest.
`Installer{Volume,GameDir string}`: `Installed()(map[string]Installation,error)`, `Recover()error`, `Preview(id string,next *Snapshot,replace bool)([]Change,error)`, `Apply(id string,next Snapshot,replace bool)error`, `Adopt(id string,next Snapshot)error`, `Uninstall(id string)error`. `Installation{Current Snapshot,Previous []Snapshot}`. `Change{Path,Action string}`. Nil next previews uninstall. Installer owns receipts/journal; startup must stop if recovery cannot complete. All filesystem operations stay within Volume, rejecting link parents. Deployment serializes under Manager.
`New(volume,gameDir string,source ReleaseSource,notice func(string))(*Manager,error)`, `Startup(context.Context)error`, `Run(context.Context)`, `Command(context.Context,string)`, `Handles(string)bool`. Startup recovers/applies pending changes and performs bounded checks. Run periodic checks; Command parses complete `egg plugins ...` lines. Notice lines use `SourceEgg | Plugins | ` and bypass console filters. Manager loads credentials when default source is nil. Console integration runs commands on its own bounded worker queue.

### Task 1: Recipes and immutable acquisition
Files: internal/plugins/model.go, provider.go, package.go and corresponding tests.
- [x] Write/run failing validation, GitHub/GitLab HTTP fixture and ZIP/tar/raw mapping tests.
- [x] Implement shared model and validation, origin-bound credentials, release/pin/asset selection, bounded downloads, safe extraction and mappings. Include token-redirect and changed-layout tests.
- [x] Run `go test ./internal/plugins` and confirm PASS.

### Task 2: Transactional deployment
Files: internal/plugins/install.go, transaction.go and corresponding tests.
- [x] Write/run failing real-filesystem install/adoption/modified-file/collision/rollback/uninstall/recovery tests.
- [x] Implement Installer interfaces; journal before writes, receipt commit, recovery, two prior snapshots, protected defaults. Verify staged file digests before changing files.
- [x] Run `go test ./internal/plugins` and confirm PASS.

### Task 3: Owner workflow and persistence
Files: internal/plugins/manager.go, commands.go, state.go and corresponding tests.
- [x] Write/run failing check→preview→approve→restart tests with real fixture packages, recipe changes, offline startup and explicit modifications decisions.
- [x] Implement Manager, strict manifests and credentials, exact-ID pending actions, adoption/rollback/removal, configurable periodic checks and bounded retained stage cleanup.
- [x] Run `go test -race ./internal/plugins` and confirm PASS.

### Task 4: Egg lifecycle and console
Files: internal/egg/run.go, plugins.go, console.go, internal/supervisor/supervisor.go and tests, docs/reference/plugins.md, README.md.
- [x] Write/run failing split-input interception, ordinary command/quit forwarding and startup notice/filter tests.
- [x] Integrate safe reserved command interception, queued command execution and cancellation, startup/periodic manager lifecycle for both modes.
- [x] Document editable manifests, three distinct package layouts, private GitLab credentials, preview/approval/restart/adoption/rollback/uninstall and limitations.
- [x] Run `go test -race ./internal/egg ./internal/supervisor ./internal/plugins` and confirm PASS.

### Task 5: Integration review and delivery
- [x] Run `bash scripts/verify.sh` and `bash scripts/test-install-node.sh`; confirm PASS.
- [x] Fresh independent review against spec, including Review Focus; resolve material findings with regressions.
- [ ] Commit, push `codex/per-server-plugin-manager`, create and attach a new PR targeting main.

## Execution record

Reuse the attached clean managed worktree at origin/main b244b50. User has approved implementation and a new PR; no further design approval is needed. Independent provider/package, transactional installation and console integration work may proceed in parallel under dispatching-parallel-agents with this shared contract. Main agent owns model/manager and integration. No deployment or live node mutation.


Rulings during integration:
- Installer accepts an optional `Preserve map[string][]string` loaded from the manifest, so keep decisions apply immediately to rollback/uninstall and persist as seed protection in receipts.
- Add `cancel <id>` so a pending action can be revoked without removing the recipe.
- Retained ownership receipts may exceed the 100 active-recipe limit; the metadata size limit still applies. Removing a recipe must not silently forget ownership.
- Package/provider/archive limits and ordinary-ZIP restrictions are explicit in the operator guide. No owner package examples were supplied, so behavior is verified with real HTTP/archive/filesystem fixtures.

Validation: initial model/manager and component regressions observed failing before implementation; integrated plugin/egg/supervisor race suite, full `scripts/verify.sh`, and bootstrap fixtures passed. Independent whole-feature review found one material issue: uninstall could lose a newly added keep rule after recipe deletion. The regression was reproduced and fixed by freezing all preview-preserved paths with the pending uninstall, merging later keep policies; both explicit keep and locally modified/reverted cases pass after recreating the Manager. The pre-existing PTY backpressure behavior was reproduced on base and left outside this change. No live game, private GitLab account or container deployment has been performed.

Known bounded-scope limitations: a crash before journal publication can leave inert transaction backup directories; a manager initialized through a console command after malformed initial configuration resumes periodic checks on the next restart. These do not change installation consistency and are not expanded into unrelated cleanup/lifecycle work.
