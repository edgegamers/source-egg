# Generic Source engine egg: proposal and delivery plan

Date: October 24, 2026. Status: approved direction; core implementation underway. See [implementation status](IMPLEMENTATION.md) for delivered behavior and remaining release work. Retail-game/client acceptance is still pending.

## Scope and recommendation

Create one Linux Pelican egg with a game dropdown for **CS:GO, CS:S, DOD:S, L4D2, and TF2**, backed by a shared Go runtime and an optional node daemon. The user confirmed **current standalone CS:GO**, rather than the older `csgo_legacy` client. This is a new Source 1 project derived selectively from the S2Script egg's operational architecture.

**Confirmed storage scope:** keep base-game VPK sharing and Metamod/SourceMod release caching. Workshop integration, custom-map caching/sharing, and custom-content download automation are deferred outside this delivery plan. Occasional CS:S event maps remain ordinary server-local uploads; reconsider automation only if usage grows.

**Confirmed password/guard scope:** support opt-in generated join passwords per game (default dev policy), per server, or per node. Game sharing is limited to participating servers on the same node. RCON remains independent. Exclude the CS2 connection/network guard entirely.

Keep the existing project's useful separation: the container installs/supervises one server; the privileged node handles discovery, shared files, fleet updates, caches, and host modules. Each selected game must be a first-class profile throughout the system. A generic image alone cannot identify which game a container runs.

Approved approach: **one egg, five explicit profiles, optional node daemon**. It provides the requested dropdown and preserves standalone use. Alternatives considered were generating five separate exports (clearer game-specific panel settings but no single selector), and standalone-only operation (simpler, without shared host storage or coordinated downloads).

Detailed supporting evidence and unresolved compatibility checks are in [the feasibility research](research/2026-10-24-source-engine-feasibility.md).

## What can be reused

The reviewed S2Script reference is commit [`8857a5519ac4cb56b26716f6d57f7956f60d3a8e`](https://github.com/s2script/s2script-cs2-egg/tree/8857a5519ac4cb56b26716f6d57f7956f60d3a8e).

- Reuse or adapt the Go supervisor, node core/module structure, bounded socket protocol, installer, Wings restart integration, release tooling, logging, and operational modules. The reference supports standalone installation plus optional node discovery and modules. [Reference README](https://github.com/s2script/s2script-cs2-egg/blob/8857a5519ac4cb56b26716f6d57f7956f60d3a8e/README.md), [module inventory](https://github.com/s2script/s2script-cs2-egg/blob/8857a5519ac4cb56b26716f6d57f7956f60d3a8e/docs/modules/README.md).
- Replace CS2-specific launch paths, app IDs, addon providers, content layouts, and console parsers. Omit the connection/network guard. S2Script, MultiAddonManager, and GameFixes are not generic Source 1 dependencies.
- Refactor container identity and update targeting. The current core discovers containers by image and stores no game profile; its VPK updater iterates every discovered container after its single central build changes. That behavior is correct only for its current single-game scope. [Discovery source](https://github.com/s2script/s2script-cs2-egg/blob/8857a5519ac4cb56b26716f6d57f7956f60d3a8e/internal/node/core/docker.go), [update loop](https://github.com/s2script/s2script-cs2-egg/blob/8857a5519ac4cb56b26716f6d57f7956f60d3a8e/internal/node/vpksync/module.go).
- Redesign shared-install updates around immutable generations. The reference updates a central directory in place and links VPKs from it; that does not provide the isolation proposed below for servers with deferred restarts. [SteamCMD updater](https://github.com/s2script/s2script-cs2-egg/blob/8857a5519ac4cb56b26716f6d57f7956f60d3a8e/internal/node/vpksync/steam.go), [file delivery](https://github.com/s2script/s2script-cs2-egg/blob/8857a5519ac4cb56b26716f6d57f7956f60d3a8e/internal/node/vpksync/push.go).

Retain upstream attribution, notices, and license headers for reused code; the reference identifies itself as GPL-3.0-or-later.

## Game profiles and panel behavior

| Selector | Dedicated download app | Game folder | Initial policy |
| --- | ---: | --- | --- |
| CS:GO | 740, subject to current-install proof | `csgo` | Standalone client/auth app 4465480; validate download access, GSLT, and client joins first |
| CS:S | 232330 | `cstrike` | Prefer tested 64-bit runtime |
| DOD:S | 232290 | `dod` | Prefer tested 64-bit runtime |
| L4D2 | 222860 | `left4dead2` | Retain 32-bit game runtime and dependencies |
| TF2 | 232250 | `tf` | Prefer tested 64-bit runtime |

IDs and architecture evidence, including the distinction between SDK support and shipped executables, are cited in [the research profile matrix](research/2026-10-24-source-engine-feasibility.md#game-profiles). Current CS:GO's client/auth identity must not be conflated with its dedicated download app, or CS2's 730.

Proposed profile fields: stable game key; server download app; client/auth app; branch; OS/architecture; executable/arguments; default map; game directory; mutable/private paths; approved base-game shared paths; console/state parser; framework compatibility policy. Resolve these from a maintained registry rather than allowing conflicting raw app IDs and folders in the panel.

Use an enumerated `SOURCE_GAME` setting. Pelican's current implementation renders an `in:` validation rule as a select, with literal values as option labels. Use recognizable game names or document the keys clearly. Pterodactyl 2 documents equivalent list rendering; older panel versions require an import/render test and may need a validated text fallback or generated per-game exports. [Pelican field source](https://github.com/pelican/panel/blob/main/app/Filament/Components/Forms/Fields/StartupVariable.php), [Pterodactyl 2 variables](https://docs.pterodactyl.io/v2/guides/egg-creation/egg-variables).

Proposed settings: game; map; hostname; slots where supported; server/join/RCON credentials; applicable GSLT; install/update toggles; Metamod and SourceMod exact pins; approved extra launch arguments. Keep complex node policies in node config. Do not assume every game accepts the same tickrate, mode, slot limit, or readiness marker. Do not expose managed Workshop or map-cache settings in this scope.

Persist the installed profile identity. Changing the selector on an existing installation should stop with a clear migration/reinstall instruction, rather than overlaying a different game onto the same directory. Automatic game conversion is outside initial scope.

## Planned feature list

| Feature | Proposed behavior | Delivery |
| --- | --- | --- |
| Unified game selector | One egg/image; five validated game profiles | Initial release |
| SteamCMD installer/updater | Install/update only selected game and branch; repair/validate on demand; support required login without assuming universal anonymous access | Initial release |
| Metamod auto-install/update | Latest stable Source 1 package by default; optional exact pin/off switch | Initial release |
| SourceMod auto-install/update | Latest prerelease package by default; optional exact pin/off switch; preserve server-owned settings | Initial release |
| Standalone operation | Own files and downloads; no daemon required | Initial release |
| Node discovery | Discover approved containers, derive selected game from inspected config, verify startup handshake and install identity | Initial release |
| Game-aware updates | Separate build state and rollout queues per game/app/branch/runtime; unrelated games receive no push/restart | Initial release |
| Restart scheduling | Immediate, empty-server, or maintenance-window policy; countdown, maximum delay, bounded concurrency | Initial release |
| Shared game content | Optional immutable per-game/build store with read-only delivery and private writable files | Initial release after Linux proof |
| Metamod/SourceMod cache | Cache only these frameworks' release metadata and archives; coalesce concurrent requests; deploy privately per server | Initial release |
| Pins and recovery | Per-server/per-game pins, retained previous framework packages, scoped canaries and health-checked rollback | Initial release |
| Discord webhooks | Game/build/addon updates, deferred restarts, start/stop/crash, failed install/push, rollback, disk/cache problems | Initial release |
| Console and diagnostics | Persistent/rotated console logs, secret masking, crash evidence, `status` and `doctor` | Initial release |
| Backups and cleanup | Back up private configs/plugins/data; omit re-downloadable shared content by default; retain content/version receipts | Initial release |
| Metrics | Game/version/generation and pending-restart status plus node/server resource metrics | Initial release |
| Generated join passwords | Opt in per server; game/server/node scopes, default game for dev; persistence, rotation, root reveal and separate private webhook | Core implementation |
| Host maintenance | Optional retention/pruning and disk notifications; independent from game updates | Follow-up |

This retains the reference's operational feature family while excluding its Workshop module and connection guard. Framework updates must preserve admins, database config, custom plugins/extensions, disabled-plugin choices, and server-generated state. Track which files the package manager owns so removed upstream files can be reconciled without deleting custom content. Initial-release rows describe the release target; delivered coverage is recorded separately in IMPLEMENTATION.md.

## Password scope

Generated join passwords require per-server opt-in (`GENERATE_PASSWORD=1`). Node default `password_scope: game` shares one assignment among participating servers of the same game on that node. Alternatives are `server` (one per UUID) and `node` (one across participating games on the node). Admin-only `PASSWORD_SCOPE` can override the default per server. Separate node state prevents sharing across nodes.

Persist cryptographically generated assignments privately and apply them at startup. Rotation requests a Wings restart only for the requesting opted-in server; other participants pick up the shared assignment through their own checks. Keep credentials out of ordinary events, status and console logs. Optional credential delivery uses a separate private webhook; root can reveal a password explicitly. Manual join passwords and RCON remain separate.

## Framework resolution and Metamod/SourceMod cache

Verified on October 24: Metamod **1.12.0.1228** is stable and has a Linux asset; SourceMod **1.13.0.7478** is a prerelease with a Linux asset. These are research snapshots, not permanent hardcoded versions. Resolve the requested channel at runtime and select an applicable Linux asset. The newest Metamod 2.x prerelease is not the requested stable channel. [Metamod release](https://github.com/alliedmodders/metamod-source/releases/tag/1.12.0.1228), [SourceMod release](https://github.com/alliedmodders/sourcemod/releases/tag/1.13.0.7478).

Recommended precedence: explicit server pin, game policy/pin, fleet default channel. A missing pin produces a visible failure/hold; it never silently becomes latest. Channel selection and compatibility validation are separate: an available archive is not proof of working binaries or third-party extensions for every profile.

Cache Metamod and SourceMod release metadata and exact archives, including provider, release/build, platform, architecture coverage, and content digest. Custom plugins, maps, VPK event packs, and Workshop items are outside this cache's scope. Use official asset identity and published integrity information where available; a locally calculated hash identifies cached bytes but does not establish upstream authenticity. Publish only complete downloads, coalesce concurrent work, retain known-good packages, and show stale metadata explicitly on upstream failure.

Deploy extracted mutable content privately. Do not share a writable `addons/sourcemod` tree, databases, logs, or all custom native extensions among servers. A binary-compatible archive can be cached once for multiple games; deployment and compatibility receipts still belong to each server. Native extension ABI and game assumptions must be checked separately.

## Preventing unrelated restarts

Persist authoritative server identity independently from socket-reported display data:

`server UUID -> game profile -> download app + branch + OS/runtime -> installed generation + framework versions`

Discover the environment through Docker/Wings and compare it with the egg's handshake and saved install record. Unknown, missing, changed, or inconsistent identities are excluded from automated rollout until resolved. Never infer game solely from a shared image name, hostname, map name, or a process called `srcds`.

For a CS:S update:

1. Check/update the CS:S installation identity under its own lock. Compare that branch's installed build/depot receipt, rather than global Steam news or another game's build.
2. Stage and verify a new CS:S generation; keep old generations immutable.
3. Select only servers matching that identity. Record a desired generation without altering a running server's files.
4. Apply each server's restart policy. Recheck profile, current generation, running state, and target before acting; deduplicate requests.
5. Stop through Wings, attach/deploy the selected generation, start, and verify game readiness and framework loading. Do not start an intentionally stopped server.
6. Emit an update result carrying the game, old/new build, affected UUIDs, and failures. TF2/L4D2/DOD:S/CS:GO receive no game update action.

Framework releases have a different subscriber set: a new SourceMod package can affect several games, but only servers following that channel and passing compatibility policy. Keep game, framework, egg image, and node self-update channels separate. Unknown player count must not be interpreted as empty; apply a documented conservative fallback and maximum-delay policy.

## Shared content: feasibility and boundaries

**VPK sharing is feasible, subject to retail-game tests.** A trusted node service can expose immutable files through read-only bind mounts and links at the paths each game expects. An egg alone cannot create privileged host mounts. Keep split VPK indexes and chunks together. [Docker bind mounts](https://docs.docker.com/engine/storage/bind-mounts/), [Valve VPK declarations](https://github.com/ValveSoftware/source-sdk-2013/blob/master/src/public/vpklib/packedstore.h).

Use a layout such as `/srv/source-shared/<game>/<branch>/<runtime>/<generation>/`, with receipts recording Steam build and depot manifests. Pin the concrete directory for the entire server process lifetime. Switching a shared `current` link while old servers run is insufficient because later file opens could see a new generation.

Share only tested immutable base-game content. Keep gameinfo/loading configuration, configs, addons, logs, demos, downloaded/custom content, Steam manifests, and mutable data private. Custom maps and event assets must never be absorbed into the shared base-game store, overwritten by its rollout, or pruned by its cache cleanup. Copy or reflink non-shared base-game files when the server is stopped. Read-only links are the preferred shared-content option; plain copies provide a compatibility fallback. Do not default to writable hardlinks, whose inode is shared across servers.

Share within the same game/build first. Cross-game deduplication is an optional hash/filesystem optimization, not a promise that similarly named Source files are interchangeable. Measure actual per-title savings; retained old/new generations increase peak disk requirements. Prune only unreferenced generations, including references from stopped servers and manual pins.

**Workshop and custom-map caching are deferred.** Do not build Workshop acquisition, polling, dependency resolution, shared custom-content stores, or event-pack installers as part of these phases. Administrators can upload and retain CS:S event maps in the server's private files. Native game behavior and manually configured client downloads remain outside the egg's managed cache. The original feasibility findings remain in the research note as background, not scheduled work.

Managed mode must not silently run SteamCMD through shared links when a daemon or mount is unavailable. Use a verified previously attached generation, wait/fail clearly, or explicitly materialize a private install before entering standalone mode. A freshly provisioned standalone server remains independent of this managed-mode rule.

## Delivery sequence and proof

1. **Compatibility proof.** Use a Linux Wings node for clean installs of all five games; prioritize standalone CS:GO authentication and L4D2's 32-bit runtime. Record actual executable paths, architecture, app/branch receipts, GSLT requirements, readiness, and successful real-client joins. Verify `meta version`, `meta list`, and `sm version` plus a map change. Confirm the game dropdown on the target panel version. This is the first gate before claiming five-game support.
2. **Standalone foundation.** Adapt the Go supervisor, introduce the profile registry, produce one egg/image, and implement safe SteamCMD and framework deployment, receipts, preserved configs, logging, and pins. Keep Source 2 installers out of the artifact.
3. **Node and update targeting.** Add trusted profile discovery, versioned handshake, separate install workers/state, targeted Wings restarts, canary/rollback records, notifications, diagnostics, and Metamod/SourceMod archive caching. Test with mixed-game fleets before enabling automatic rollouts.
4. **Shared storage.** Implement generation staging, immutable mounts, private file delivery, references, disk checks, and garbage collection. Test old and new generations concurrently and verify no active server observes files from the other generation.
5. **Operational parity and release.** Finish backup/cleanup/metrics/crash bundles and user docs; publish versioned/stable/beta artifacts through CI. Add optional operational modules as their separate acceptance tests pass. Workshop, custom-map caching and the connection/network guard are outside this implementation.

High-value acceptance cases: a CS:S-only update never changes/restarts TF2; each branch and runtime stays isolated; stopped servers remain stopped; failed/partial downloads are never published; interrupted rollouts recover; unresolved game identity receives no update; missing mounts cannot corrupt shared installs; package upgrades preserve custom config; rollback selects a retained generation; garbage collection preserves active and pinned content; uploaded CS:S event maps and assets survive base-game updates and cache cleanup. Include real client joins and map loads in both standalone and managed modes for every supported profile.

## Feasibility decision

- **High confidence:** one selectable egg, profile registry, game-scoped update/restart decisions, Metamod/SourceMod archive caching, webhooks, supervisor and operational features.
- **Feasible with node proof:** read-only VPK sharing, immutable generations, controlled generation rollout, per-title runtime/framework compatibility.
- **Conditional:** current standalone CS:GO download/authentication details; these must not be advertised as validated from documentation alone.
- **Deferred/out of scope:** Workshop integration, custom-map caching/sharing, and custom-content download automation. Their feasibility does not block the planned release.
- **Excluded:** the CS2 connection/network guard.

This work is substantially more than adding four app IDs to the CS2 egg. The reusable operational foundation is valuable; the main new work is explicit profiles, Source 1 compatibility, and safe storage/update transactions. No reliable delivery estimate should be attached until the first compatibility proof resolves these unknowns.
