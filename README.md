# Source Engine Egg

A Source engine egg for Pelican and Pterodactyl, using the same container/node setup pattern as [S2Script CS2 Egg](https://github.com/s2script/s2script-cs2-egg). Choose **CS:GO, CS:S, DOD:S, L4D2, or TF2** from one game dropdown.

Included: latest stable Metamod:Source, latest SourceMod prerelease, shared base-game VPKs, central framework caching/pins, game-specific updates/restarts, Discord events, password scopes, node console archives and crash bundles. Optional node modules provide backups/restore, cleanup, resource metrics/Prometheus/top, host maintenance and verified node self-updates. Workshop/map caching and the CS2 connection guard are excluded.

Release versions come from the GitHub release tag. The bootstrap selects a published stable release and the egg defaults to `:stable`; use `:beta` for testing releases. **Wait for the release workflow to publish the node assets and image before installing.** This is an initial implementation; [live-game acceptance and remaining features](docs/IMPLEMENTATION.md) are tracked separately.

## 1. Set up the node (optional)

For shared game files, do this **before creating servers**. Run on the Wings node, not the web-panel machine:

```bash
curl -fsSL https://raw.githubusercontent.com/edgegamers/source-egg/main/scripts/install-node.sh | bash
```

The bootstrap verifies the release checksum, installs missing host SteamCMD runtime libraries on Debian/Ubuntu, and opens the **Source node setup wizard** with sudo. Choose VPK sharing, framework caching and game predownloads, then restart/password/webhook policies and optional operations modules. No Go or Python is needed on the node. Host maintenance starts in dry-run mode.

Use an interactive SSH terminal on Linux x86-64 with Docker/Wings, sudo access, kernel 5.12+, and enough disk for the selected games plus update staging. Debian 12+ or Ubuntu 22.04+ is recommended. The node can be installed with **zero servers**: choose games to predownload, or `none` to download each game when its first server starts.

For beta:

```bash
curl -fsSL https://raw.githubusercontent.com/edgegamers/source-egg/main/scripts/install-node.sh | SOURCE_NODE_CHANNEL=beta bash
```

For an unattended install, append `| bash -s -- --yes` instead of `| bash`; it retains existing settings or uses defaults. An exact initial tag can be selected with `SOURCE_NODE_RELEASE=v1.2.3`. [Node setup and update guide](docs/node.md).

The daemon discovers matching containers automatically, including servers created later. There is no per-server registration command or manual panel mount.

```bash
sudo sourcenode status
sudo sourcenode doctor
journalctl -u sourcenode -f
```

Wait for any selected game predownloads to finish before creating servers. Rerun `sudo sourcenode install` to change wizard choices; rerun the verified bootstrap to upgrade the node binary while keeping saved configuration.

## 2. Import the egg in the panel

In Pelican's admin egg importer, choose **Import from URL** and paste:

```text
https://raw.githubusercontent.com/edgegamers/source-egg/main/pterodactyl/source-egg.json
```

For Pterodactyl, download the JSON and import the file if your panel has no URL option. The export includes the square Source-logo picture for Pelican.

Create a server, select its **Game** before the first start, allocate the game port and any required extra ports, and set `STEAM_GSLT` where needed. Current standalone CS:GO uses client/auth app **4465480** and dedicated download app **740**. Do not select CS2's legacy branch.

Use `ghcr.io/edgegamers/source-egg:stable` for stable releases, or `:beta` for the newest published release including prereleases. Start it: the entrypoint installs the selected game, Metamod and SourceMod. The short panel installation script is intentional; with node-managed files, the container skips its own SteamCMD game download.

Prefer standalone? Skip step 1. `SOURCE_MODE=auto` installs into the server's own volume when no node is present. An already shared installation requires a working node/mount and does not silently write through shared links. [Server installation guide](docs/install.md).

## 3. Choose framework and password settings

| Panel variable | Default | Purpose |
|---|---|---|
| `SOURCE_GAME` | `CS:S` | Select CS:GO, CS:S, DOD:S, L4D2 or TF2 before installation |
| `INSTALL_METAMOD` | `1` | Install/check latest stable Metamod |
| `METAMOD_VERSION` | empty | Optional exact upstream release tag |
| `INSTALL_SOURCEMOD` | `1` | Install/check latest SourceMod prerelease |
| `SOURCEMOD_VERSION` | empty | Optional exact upstream release tag |
| `STEAM_GSLT` | empty | Game Server Login Token where required |
| `GENERATE_PASSWORD` | `0` | Opt into node-generated join passwords for dev servers |
| `PASSWORD_SCOPE` | `default` | Follow node policy or select `game`, `server`, or `node` |

SourceMod automatically requires Metamod. Framework upgrades preserve private configs, custom changes and disabled stock plugins. Exact version pins never silently become latest.

The default dev password policy is **one join password per game on each node**. Alternatives are one per server or one across all participating servers on a node. Password generation is opt-in; RCON credentials remain separate.

```bash
sudo sourcenode passwords show FULL_SERVER_UUID
```

Ordinary webhooks exclude passwords. Optional password delivery uses a separately configured private webhook. [Passwords, pins, rollback and operations](docs/install.md).

For a dev node sharing one public address, use Docker bridge networking with separate allocated ports. Keep an existing working Wings network configuration.

[Node guide](docs/node.md) · [Server/operations guide](docs/install.md) · [Build and test from source](docs/development.md) · [Implementation status](docs/IMPLEMENTATION.md)

[Node operations and full CLI](docs/operations.md) · [Complete configuration reference](docs/reference/node-config.md). Operational parity changes require a release containing this implementation; existing released binaries retain their original feature set. Live five-game acceptance remains a separate release gate.

GPL-3.0-or-later. Runtime helpers retain attribution to K4ryuu/CS2-Egg-Go and S2Script contributors; see [LICENSE](LICENSE) and [NOTICE](NOTICE).

## Publishing a release

Create a GitHub release from the intended commit with a tag such as `v1.2.3`. Leave **Set as a pre-release** unchecked for stable, or check it for a test release such as `v1.3.0-rc.1`. Publish and wait for Actions to finish.

Actions tests the code, builds both programs with the tag's version, attaches the node binary/checksums and egg JSON, and publishes the versioned image plus eligible `stable`/`beta` aliases. No per-release version-file or installer edits are needed. Publishing a release is separate from pushing source code.
