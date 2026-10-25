# Source engine egg: compatibility and storage research

Research date: October 24, 2026. Scope: Linux amd64 Pelican/Pterodactyl hosting for CS:GO, CS:S, DOD:S, L4D2, and TF2. The user confirmed **current standalone CS:GO**; the older CS2 `csgo_legacy` client is out of scope. This is research and proposed engineering policy, not a claim that live installations have been tested.

**Scope decision, October 24:** retain base-game VPK sharing and Metamod/SourceMod caching. Defer Workshop integration and custom-map caching/sharing; occasional CS:S event maps will remain server-local uploads. Workshop/custom-content findings below are background research, not planned delivery work. See the [updated proposal](../source-egg-proposal.md).

## Findings

A single egg is feasible if it selects a maintained game profile rather than treating all Source games as interchangeable installations. Separate each game's installation identity, client/authentication identity, game directory, runtime architecture, startup parameters, and base-game shared-file policy.

The main compatibility uncertainty is CS:GO's current standalone client and authentication behavior. The main storage constraint is that shared files must be immutable while any server uses them. Download caching is easier and lower risk than sharing a complete live addon directory.

## Game profiles

| Profile | Dedicated server download App ID | Game directory | Proposed initial Linux runtime |
| --- | ---: | --- | --- |
| Current standalone CS:GO | 740 candidate, supported by current upstream tooling; clean Steam install untested | `csgo` | Inspect installed server ELF and test; do not infer runtime from available plugin SDK targets |
| CS:S | 232330 | `cstrike` | Prefer x86_64 after smoke test |
| DOD:S | 232290 | `dod` | Prefer x86_64 after smoke test |
| L4D2 | 222860 | `left4dead2` | x86; retain 32-bit runtime libraries |
| TF2 | 232250 | `tf` | Prefer x86_64 after smoke test |

The AlliedModders setup guide documents all five download IDs and game directories. Its CS:GO discussion predates the 2026 standalone listing, so it establishes the historical dedicated server profile rather than today's complete authentication recipe. [AlliedModders setup guide](https://wiki.alliedmods.net/User:Nosoop/Guide/Setup)

Current AlliedModders SDK manifests confirm those five dedicated app IDs. Their Linux targets include x86 and x86_64 for CS:S, DOD:S, TF2, and CS:GO, but only x86 for L4D2. SDK target availability establishes plugin build coverage; it does not prove a shipped dedicated server binary uses every listed architecture. The CS:GO manifest also declares `login_required: true`, so anonymous download availability must be tested rather than promised for every title. [Pinned manifests used by current SourceMod](https://github.com/alliedmodders/hl2sdk-manifests/tree/931f7aa57823c3bcf34f1867e28d8d12769a8ba7/manifests)

Valve explicitly added Windows/Linux 64-bit client and server support to TF2 on April 18, 2024. [Valve TF2 release note](https://www.teamfortress.com/post.php?id=220436)

**Proposal:** ship a Linux amd64 image containing both 64-bit game dependencies and the 32-bit loader/libraries needed by SteamCMD and older Source runtimes. Detect the profile's expected executable and validate its ELF architecture at install/startup. Do not use one assumed `srcds_linux64` path for all titles. Do not expose a generic tickrate or player-limit promise without game-specific validation.

## Current standalone CS:GO is the selected scope

Valve now publishes a separate CS:GO store entry under **4465480**. App 730 cannot simply be assumed to mean CS:GO, because it is also the Counter-Strike 2 application. [Valve's current CS:GO store entry](https://store.steampowered.com/app/4465480/CounterStrikeGlobal_Offensive/)

LinuxGSM's current first-party configuration uses dedicated download app **740**, and separately configures `clientappid=4465480`. It documents 730 as the option for clients using CS2's `csgo_legacy` beta. Its configuration requires a GSLT matching the selected client app. [LinuxGSM CS:GO implementation](https://github.com/GameServerManagers/LinuxGSM/blob/master/lgsm/config-default/config-lgsm/csgoserver/_default.cfg)

**User choice:** support the current standalone CS:GO client. The older 730 `csgo_legacy` path is historical context and is not a requested alternative or test target.

**Proposal:** use server-download app 740 and advertised/authentication app 4465480 as the initial profile, following current LinuxGSM implementation. These values remain subject to an actual clean installation and client connection test; the Valve store listing alone does not establish the dedicated-server recipe. Keep server-download ID, branch, and advertised/authentication ID as separate internal fields. Reapply managed CS:GO metadata after update/validation only after the tested standalone recipe is established.

**Not yet verified:** a clean SteamCMD install, the exact current app 740 branch/content, anonymous versus authenticated download access, standalone-client join/authentication, GSLT registration for the standalone client, and whether NoLobbyReservation is still required. App 740 is verified in current upstream configuration, not proven by a Steam download in this research. Do not install arbitrary binary patch packs based on community troubleshooting advice. Any compatibility plugin must be selected from its upstream source and tested.

## Metamod and SourceMod channels

At research time, upstream GitHub release metadata identifies **Metamod:Source 1.12.0.1228** as `prerelease=false` and provides `mmsource-1.12.0-git1228-linux.tar.gz`. The newer 2.0 releases are marked prerelease. Therefore selecting the numerically highest Metamod version would not implement the requested stable channel. [Metamod stable release](https://github.com/alliedmodders/metamod-source/releases/tag/1.12.0.1228), [release metadata](https://api.github.com/repos/alliedmodders/metamod-source/releases?per_page=3)

SourceMod's official development download page currently selects **1.13 build 7478**; the matching release is **1.13.0.7478**, marked prerelease, with a Linux archive. The page cautions that development builds may introduce breaking changes. [Official SourceMod dev downloads](https://www.sourcemod.net/downloads.php?branch=dev), [matching release](https://github.com/alliedmodders/sourcemod/releases/tag/1.13.0.7478)

SourceMod's current dependency checkout includes CS:GO, CS:S, DOD:S, L4D2, and TF2 SDKs. Its release workflow enumerates applicable non-Source2 manifests and builds x86/x86_64 targets. This does **not** support a claim that current 1.13 universally removed CS:GO support. It also does not prove every game currently boots with the selected archive. [Dependency checkout](https://github.com/alliedmodders/sourcemod/blob/master/tools/checkout-deps.sh), [release workflow](https://github.com/alliedmodders/sourcemod/blob/master/.github/workflows/build-release.yml)

**Proposal:** resolve Metamod stable and SourceMod prerelease from official metadata, require a matching Linux asset, cache by exact release and checksum, and record the chosen versions. Use profile-specific compatibility ceilings only when backed by a reproduced failure. Permit manual pins and retain a known-good prior package. A newly published package should first pass a canary server for every affected runtime/profile.

SourceMod's package includes runtime binaries, default plugins, configs, gamedata, translations, and writable directories. Consequently, mounting the complete unpacked package read-only is not a substitute for separating package files from each server's configuration and mutable state. [SourceMod package definition](https://github.com/alliedmodders/sourcemod/blob/master/tools/buildbot/PackageScript)

## VPK sharing

**Feasible at the filesystem/container layer; game-by-game runtime validation remains necessary.**

Valve's SDK gameinfo example mounts VPKs and subdirectories through search paths, explains that custom content is scanned at boot, and gives VPKs precedence over ordinary folders. It also separates downloaded content and write paths from packaged content. This is evidence for Source content mounting behavior, not a guarantee that every retail title accepts an identical `gameinfo.txt` modification. [Valve SDK example](https://github.com/ValveSoftware/source-sdk-2013/blob/master/game/mod_tf/gameinfo.txt)

Valve's packed-store header represents VPK file entries with archive numbers and offsets and defines multi-chunk archive metadata. Treat a split VPK index and its referenced chunks as a consistent package. [Valve VPK declarations](https://github.com/ValveSoftware/source-sdk-2013/blob/master/src/public/vpklib/packedstore.h)

Docker supports binding host content into containers read-only. Host paths must exist on the Docker daemon's node, and read-only bindings prevent containers from changing the shared cache. These are node-level capabilities: an egg alone cannot grant itself arbitrary host mounts. [Docker bind mount documentation](https://docs.docker.com/engine/storage/bind-mounts/)

**Recommended storage design:**

- Maintain immutable game generations keyed by `(game, server app, branch, OS, architecture, build/depot identity)`. Keep all VPK chunks from the same generation together.
- Expose the trusted cache to servers with an administrator-managed read-only mount. Link only profile-approved, immutable assets into their expected locations, or use filesystem reflinks when supported.
- Keep configs, addons, SourceMod data/logs/databases, downloads, custom/event maps and assets, demos, generated files, and Steam manifests private. Base-game rollout and cache pruning must preserve manually uploaded event content.
- Never point running servers at a mutable `current` path whose targets can change between file opens. Resolve/pin a concrete generation at startup, keep it until process exit, then release it.
- Update by creating and validating a new generation; roll eligible servers to it after stopping them. Retain generations referenced by running or manually pinned servers.
- Deduplicate across games only by verified identical content hashes or filesystem deduplication; a shared filename or engine family does not prove identical bytes or compatibility.

These bullets are engineering recommendations, not a tested implementation. Symlink support, permissions, managed mounts, backup behavior, disk accounting, VPK loading, and actual savings require a Linux node trial. Hardlinks expose the same inode, so accidental in-place writes can affect every linked copy; they should not be the default for writable deployment targets.

## Metamod/SourceMod caching and deferred custom-content research

**In scope:** cache Metamod and SourceMod release metadata and exact archives centrally, then deploy private mutable settings/state per server. Custom addon archives, event packs, and maps are outside this cache's scope.

**Background feasibility only:** downloaded custom-content archives could be cached, while generic Workshop acquisition is conditional. The following findings are retained for a possible future reassessment and are not implementation requirements.

Valve's Workshop implementation guide supports downloading and temporarily caching an item for an anonymous game server, but its modern `ISteamUGC::DownloadItem` flow explicitly does not work for legacy `ISteamRemoteStorage` items. The guide also says item-download callbacks must be checked against the application ID. This rules out assuming one universal Workshop API/behavior across these games. [Valve Workshop implementation guide](https://partner.steamgames.com/doc/features/workshop/implementation)

Valve's L4D addon announcement describes VPK distribution and matchmaking for servers with the campaign installed. It supports the model of matching server/client addon availability; it is not evidence for a generic cross-game Workshop installer. [Valve L4D addon announcement](https://www.l4d.com/blog/post.php?id=2599)

**Current cache scope:** cache Metamod and SourceMod release archives centrally. Extract/package them as immutable releases and deploy a private copy of mutable defaults. Preserve old framework versions for rollback, coalesce concurrent downloads, verify before publishing, and apply retention limits.

**If custom-content caching is revisited:** use per-game destination adapters, verify real Workshop item types and acquisition methods, and identify cached Workshop content by `(consumer app, item ID, revision/update identity, checksum)`, including dependencies. A collection ID alone does not identify immutable bytes. Validate consumer app before installation, retain referenced versions, and never mutate live shared files. No such adapters or stores are scheduled now.

Server-side caching does not install custom content on players' machines. Native Workshop subscriptions, game-specific downloads/FastDL, and client content compatibility need their own acceptance checks. Do not treat SourceMod `.smx` plugins, native `.so` extensions, VPK content packs, and Workshop collections as one interchangeable addon type.

## Required proof before claiming support

- Clean install and connect a real client for each of the five games; CS:GO acceptance must use the current standalone client, with successful Steam authentication and matching GSLT.
- Confirm selected executable architecture and that `meta version`, `meta list`, and `sm version` work; test a normal map change and restart.
- Compare local versus shared VPK startup/map load and verify a clean client can join. Use a split VPK where the title ships one.
- Run concurrent servers on old/new generations; prove updating one does not alter files opened by the other.
- Exercise CS:S-only update discovery with simultaneous TF2 and other game servers; verify no unrelated restart or version change.
- Exercise interrupted downloads, corrupt archives, missing mount, failed plugin canary, rollback, and cache garbage collection with a pinned generation.
- Test Metamod/SourceMod cache hits, concurrent downloads, and upstream failure; verify behavior when Steam authentication is unavailable. Confirm uploaded CS:S event maps and assets survive base-game updates and shared-store cleanup. Workshop and custom-map cache tests are outside the current scope.

Several Valve Developer Community pages returned HTTP 403 during this research. Where possible the report uses Valve's public SDK/source and official Steamworks documentation instead; blocked wiki pages are not treated as read evidence.
