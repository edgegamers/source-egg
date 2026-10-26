# Upstream Fork Parity Implementation Plan

> **For agentic workers:** Use focused parallel implementation under superpowers:dispatching-parallel-agents, with test-driven-development and one integrated review.

**Goal:** Deliver the approved upstream parity gaps, cron password schedules, and branded terminal wizard.

**Architecture:** Preserve Source's Go packages and immutable game generations. Port operator-facing upstream behavior through typed config and narrow package boundaries. Independent workers own password, delivery, and server features; the coordinator owns wizard, CLI, defaults/migration integration and docs.

**Tech Stack:** Go 1.27, existing PTY/x/sys dependencies, embedded terminal artwork, rooted filesystem operations.

**Spec:** ../specs/2026-10-26-upstream-fork-parity-design.md

## Global Constraints

- Reference upstream 8857a5519ac4cb56b26716f6d57f7956f60d3a8e; local read-only checkout /home/shookeagle/Projects/s2script-cs2-egg-1.
- Work only in the attached source-upstream-parity worktree. Preserve existing saved config, credentials, scopes and filesystem protections.
- No live node installation, deployment, host pruning, release publication or retail-game mutations.
- Standard five-field cron and explicit timezone; keep legacy schedule compatibility and persisted assignments.
- User selected Source 1 artwork alongside EdgeGamers. No runtime image tool required.

## Review Focus

- Existing config and headless reruns retain disabled modules, custom rules, saved secrets and pins.
- Cron invalid dates, leap years, DST transitions, offline catch-up and changed schedules cannot cause loops or credential loss.
- Alternate VPK delivery never writes through shared symlinks or crosses filesystem roots; hardlink failures are explicit.
- Terminal output sanitizes server-controlled text and hides all credentials, including summaries.
- Standalone cleanup only runs when enabled and not handled by the node; no custom configs/assets are blanket-deleted.

## Tasks

### 1. Password schedules and controls

Files: internal/passwords/*, new internal/node/password_config.go; coordinate Config fields and daemon initialization with parent.
Produces: Options Cron/Timezone/Wordlist and Next schedule helper, config master enable/path fields through parent integration.
- [x] Add failing behavior tests for cron boundaries/DST/validation, persisted due dates, custom dictionary validation and disabled module behavior.
- [x] Implement schedule and dictionary support, retain legacy interval and calendar behavior; document exact integration fields.
- [x] Run password and affected node tests; report results.

### 2. Game-delivery parity

Files: internal/content/*, internal/node/{rollout,mount*,protocol,daemon}.go and new delivery config helpers. Coordinate config definitions with parent.
Produces: configurable symlink/hardlink/copy, worker bound, validation and auto-restart behavior.
- [x] Add failing tests for alternate delivery, unsupported hardlinks, no-restart policy and upstream empty-or-window semantics.
- [x] Implement controls preserving immutable generations and secure mount/paths.
- [x] Run content/node tests and report settings and migration requirements.

### 3. Server feature parity

Files: internal/egg/*, internal/game/launch.go, internal/addons/deploy.go as needed, pterodactyl/source-egg.json, new server option helpers/tests.
Produces: standalone cleanup fallback, log filtering/level settings, argv parser, remote-debug opt-in, SourceMod plugin toggles.
- [x] Add failing tests for cleanup fallback and rooted rules, quoted args, filters, plugin enable/disable preservation and debug launch.
- [x] Implement Source 1 equivalents with existing framework/custom-file protections.
- [x] Run egg/addons/game tests and validate egg JSON.

### 4. Wizard, artwork, CLI and migration

Files: internal/nodeinstall/*, new internal/terminalui/*, cmd/sourcenode/*, internal/node/config.go, operations/observability config, docs and assets.
Consumes: config names supplied by tasks 1–3; terminal API owned solely here.
- [x] Add behavior tests covering editing all retention controls, cancellation before side effects, preserving hidden settings/secrets, and human/JSON output.
- [x] Implement upstream question sections/help/summary/progress and settings; embed terminal logos derived from supplied/repository artwork.
- [x] Match fresh-install defaults using an explicit fresh config path while LoadConfig keeps legacy defaults for omitted values.
- [x] Add readable operator tables and JSON option; validate terminal previews at narrow/wide sizes.
- [x] Update reference docs and all previously approved gap entries.

### 5. Integration and review

- [x] Reconcile config interfaces and complete unimplemented integration seams before declaring parity.
- [x] Run full Go tests/race/vet/format/build and installer fixtures; run container smoke if Docker access is available.
- [x] Dispatch independent whole-change review, fix substantive findings with regressions, and rerun affected checks.
- [x] Report final scope, test evidence, banner preview and material limitations.

## Execution ledger

- Scope: user's approval of the detailed comparison authorizes implementation; no duplicate approval round. Parallel workers are explicitly prescribed by the applicable dispatching skill.
- Worktree: /home/shookeagle/.codex/worktrees/source-upstream-parity/source-egg, detached from 7cc3e68.
- Environment: host Go is absent; use checksum-verified temporary Go 1.27.3. Docker socket denies current user and sudo requires password; continue with native toolchain and fixture tests.

- Password implementation: cron fields/ranges/steps/names, IANA timezone and DST gaps/repeats, Cronie day-field semantics, leap-century schedules, persisted due dates (including no-expiry), one overdue catch-up and custom dictionary validation. Node master enable and storage path integrated.
- Delivery implementation: three VPK methods, coalesced/bounded preparation, explicit cross-filesystem hardlink failure, stable non-root process ownership checks, Steam validation, auto-restart switch and empty-or-window behavior. Old-node/image negotiation is explicit.
- Server implementation: editable startup cleanup, console filter/level files, native argv/debug launch, gdbserver image dependency and 18 SourceMod switches (17 applicable upstream controls plus MapChooser dependency).
- Operator implementation: full wizard settings, review/cancellation before writes, fleet-pin preservation, fresh-versus-legacy defaults, terminal logos, typed CLI tables and global JSON mode. Guides and attribution updated.
- Independent review reproduced four defects: password storage alias into public mounts, cached credential startup while node generation is disabled, relocated removed stock plugins, and console filtering of the Wings readiness marker. Each has regression coverage and a fix; the reviewer reran the original harness and focused regressions and found all four resolved.
- Parent verification: `bash scripts/verify.sh` passed on the completed tree (full Go race suite, vet, gofmt, Linux sourceegg/sourcenode builds, shell syntax and SteamCMD seed fixtures). Log: `/tmp/source-parity-final-verify.log`.
- Bootstrap verification: `bash scripts/test-install-node.sh` passed non-root pipeline, stable/beta/pin, checksum/assets failures, TTY, dependencies and local-binary fixtures. No host service was installed or restarted.
- Visual verification: rendered and inspected the actual wizard transcript with the supplied EdgeGamers mark and existing Source 1 icon. Preview: `dist/wizard-preview.png`; compact banner: `dist/banner-preview.png`. Generator output is gofmt-clean and has no runtime image dependency.
- Known preexisting verification issue: independent repeated node shutdown tests reproduced an initial-discovery cancellation race in unchanged code; the final complete verification run passed. No new parity issue remains from review.
- Limits: Docker's socket denies this user and noninteractive sudo requires a password. Updated image smoke, real retail games, debugger attachment and live panel/Wings acceptance were not run. Earlier baseline image evidence is identified separately in IMPLEMENTATION.md.
- Delivery: changes remain local and reviewable in the attached worktree; no commit, merge, remote push, release or deployment performed. The original main checkout remains clean.
