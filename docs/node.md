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
| Node releases | Stable/beta/dev channel, automatic updates and check interval |
| Base-game files | Shared directory, SteamCMD path/validation, symlink/hardlink/copy delivery and worker count |
| Games to prepare now | Comma-separated `csgo,css,dods,l4d2,tf2`, `all`, or `none` to wait for the first server |
| Game updates | Check interval, automatic restart switch, immediate/empty/window policy, delay and countdown |
| Metamod/SourceMod cache | Directory, metadata refresh, archive retention and exact fleet versions |
| Passwords | Master switch, game/server/node scope, token/EFF phrases, custom dictionary and private directory |
| Password rotation | Daily/weekly/bi-weekly/monthly, custom hours/months, never, or five-field cron with timezone |
| Discord | Ordinary/private webhooks, event selection, node name, bot username and avatar |
| Console/crash storage | Directories, retention ages and crash count |
| Backups | Directory, daily hour, include/exclude patterns and independent age/count retention |
| Cleanup | Interval, startup/open-file switches, and enable/age settings for every saved rule |
| Metrics | Listener and sample interval |
| Host maintenance | Interval, dry run, image/build-cache/journal tasks, retention and free-disk threshold |

The wizard opens with EdgeGamers and Source 1 terminal artwork, then presents sections, inline help, a complete settings summary, installation progress and useful commands. `NO_COLOR` disables ANSI color; narrow terminals stack the logos and tables, and ASCII terminals use a text banner. There are no CS2 Workshop/map-cache or connection-guard questions.

Existing settings are defaults on reruns; stored credentials are hidden, custom cleanup rules and per-server framework exceptions are retained. Configuration is written only after accepting the summary. EOF or declining the review cancels setup.

Fresh installs enable shared files, framework caching, backups, cleanup, console/crash archives, metrics, automatic node updates and dry-run maintenance. Fresh game-update defaults are a one-minute check and immediate restarts; the review lets you change them. Password management requires explicit node opt-in and defaults to a per-server, three-word EFF phrase with monthly rotation when enabled. Existing installations retain their previous module and password choices.

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

Upgrade the node with `sudo sourcenode self-update`, or rerun the verified bootstrap. `self-update --check` checks without installing; `--channel stable|beta` and `--release TAG` override selection. Scheduled self-updates are optional in the wizard/config. `sudo sourcenode update` also updates the node; `sudo sourcenode update css` updates CS:S game files. Existing settings survive upgrades.

These operations use the [full command guide](operations.md) and [configuration reference](reference/node-config.md). Fresh maintenance is enabled in dry-run mode; existing installations keep their saved choice. The `dev` channel is for manually supplied binaries and performs no release lookup unless an exact release is explicitly selected. `sudo sourcenode uninstall --yes` removes the service/binary while retaining game/shared/private data; `--purge` additionally removes only the config file.

Setup reuses an existing SteamCMD installation. If that installation is damaged or incomplete, repair it before rerunning; the installer does not yet diagnose every existing-client corruption case. Managed directories must use real dedicated paths, and private node state must stay outside shared/cache roots.

For an unpublished local build:

```bash
sudo bash scripts/install-node.sh --local ./dist/sourcenode
# Or accept defaults/saved settings:
sudo bash scripts/install-node.sh --local ./dist/sourcenode --yes
```

Building is a developer task; ordinary users install published assets. [Build guide](development.md) · [Configuration, passwords and rollback](install.md) · [Implementation status](IMPLEMENTATION.md).
