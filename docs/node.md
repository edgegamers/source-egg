# Set up the Wings node

`sourcenode` is the optional host daemon for shared base-game VPKs, Metamod/SourceMod caching, game-specific restarts, Discord events and scoped join passwords. Install it on each Wings game node; the web panel can be elsewhere. **Zero existing servers is supported.**

## 1. Run the installer

After the release workflow has successfully published the image, node binary and checksums:

```bash
curl -fsSL https://raw.githubusercontent.com/edgegamers/source-egg/main/scripts/install-node.sh | bash
```

Use an interactive SSH terminal on Linux x86-64, with Docker/Wings installed, kernel 5.12+, and sudo access. Debian 12+ or Ubuntu 22.04+ is recommended. The bootstrap installs missing `jq` and host SteamCMD runtime libraries through apt, resolves one release tag, downloads its matching binary/checksum, verifies the binary, and runs the Go wizard with sudo. No Go/Python installation is required on the node.

For beta (newest published release including prereleases):

```bash
curl -fsSL https://raw.githubusercontent.com/edgegamers/source-egg/main/scripts/install-node.sh | SOURCE_NODE_CHANNEL=beta bash
```

For an exact initial tag, use `SOURCE_NODE_RELEASE=v1.2.3 bash` at the end of the pipe. Selecting a prerelease records the beta channel. A release with missing assets fails before running its binary; wait for Actions to finish and retry.

For automation with root or passwordless/cached sudo:

```bash
curl -fsSL https://raw.githubusercontent.com/edgegamers/source-egg/main/scripts/install-node.sh | bash -s -- --yes
```

`--yes` retains existing settings or uses defaults, with the selected release channel applied. Interactive mode reads `/dev/tty`, so the wizard still works when the script arrives through a pipe. Non-Debian/Ubuntu hosts need manual dependency provisioning and the verified binary's `sudo ./sourcenode_linux_amd64 install` command.

## 2. Answer the wizard

| Choice | Purpose |
| --- | --- |
| Base-game VPK sharing | One immutable base-game store per node, with private server configs/addons |
| Games to prepare now | Comma-separated `csgo,css,dods,l4d2,tf2`; accepts display names too; `none` waits for the first server |
| Metamod/SourceMod cache | Download release metadata/archives once and deploy private copies |
| Shared/cache directories | Dedicated directories with enough room for games, retained generations and staging |
| Restart policy | Immediate, empty-server, or a maintenance window, with maximum delay/countdown |
| Password scope | `game` (default dev), `server`, or `node`; each server must still opt in |
| Password rotation | Hours until regeneration; zero disables expiry |
| Discord events | Optional ordinary event webhook |
| Private password webhook | Optional separate credential delivery; ordinary events exclude passwords |

Only the features implemented for this Source egg are offered. There are no Workshop/map-cache or connection-guard questions. Existing settings are defaults on reruns; stored webhook URLs are not echoed. The wizard gathers/validates answers before saving configuration.

Installation writes `/etc/sourcenode/config.json`, installs `/usr/local/bin/sourcenode`, provisions host SteamCMD if needed, and enables/restarts `sourcenode.service`. Generated passwords remain off on individual servers until `GENERATE_PASSWORD=1` is set in the panel.

To change choices later:

```bash
sudo sourcenode install
```

An unattended reconfiguration retaining current values is `sudo sourcenode install --yes`. Configuration is private and replaced atomically. Manual advanced settings, including image discovery, host Steam credentials and custom Wings config path, are retained.

## 3. Wait for selected games, then create servers

```bash
journalctl -u sourcenode -f
sudo sourcenode status
sudo sourcenode doctor
```

Selected public-branch games prepare in the background, with a `shared <game> build <id> is ready` journal message when finished. No selected games means no initial game downloads. With zero servers, an empty server list is normal. Prefetch failures are logged; first server startup can retry after the cause is corrected.

Then [import the egg and create a server](install.md). The daemon discovers the selected game from each matching container, mounts its broker/shared/cache paths, and coordinates only that game/branch/runtime. There is no manual UUID registration or panel mount. A running server needs stop/start to pick up mounts after the daemon is first installed.

Keep the default discovery repository `ghcr.io/edgegamers/source-egg` unless using a custom image. Each server's map, configs, plugins, generated data and event maps remain private.

## Updates and local builds

To upgrade the node binary, rerun the verified bootstrap; existing settings become wizard defaults. Repeat the beta command to select beta, or use an exact tag for a controlled upgrade. The selected channel is recorded in config; automatic node self-updates are not implemented. `sudo sourcenode update css` updates **game files**, not the node binary.

Setup reuses an existing SteamCMD installation. If that installation is damaged or incomplete, repair it before rerunning; the installer does not yet diagnose every existing-client corruption case. Managed directories must use real dedicated paths, and private node state must stay outside shared/cache roots.

For an unpublished local build:

```bash
sudo bash scripts/install-node.sh --local ./dist/sourcenode
# Or accept defaults/saved settings:
sudo bash scripts/install-node.sh --local ./dist/sourcenode --yes
```

Building is a developer task; ordinary users install published assets. [Build guide](development.md) · [Configuration, passwords and rollback](install.md) · [Implementation status](IMPLEMENTATION.md).
