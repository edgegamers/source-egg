# Source Egg

A Linux Source engine egg for **current standalone CS:GO, CS:S, DOD:S, L4D2, and TF2**, with one game dropdown and an optional Wings-node daemon.

The core implementation includes game-specific SteamCMD installation, immutable base-game VPK sharing, Metamod stable/SourceMod prerelease caching and startup updates, targeted game restarts, generation pins/manual rollback, scoped join passwords, Discord events, bounded console logs and crash reports. **Workshop/map caching and the CS2 connection guard are excluded.**

This is an initial implementation, not a production-validated release. Real game installations/client joins and live Pelican import/rollout acceptance are still required. [Implementation status and remaining work](docs/IMPLEMENTATION.md).

## Build and test

Use Linux amd64 and Go 1.27+:

```bash
bash scripts/verify.sh
docker build -t source-egg:dev .
bash scripts/smoke-container.sh source-egg:dev
```

The smoke test uses a fixture server under a read-only root and arbitrary UID, without downloading a retail game. Optional proofs:

```bash
SOURCE_EGG_UPSTREAM_TEST=1 go test -v ./internal/addons -run TestOfficialLinuxPackages
# Isolated test environment with CAP_SYS_ADMIN only:
SOURCE_EGG_MOUNT_TEST=1 go test -v ./internal/node -run TestReadOnlyMountTargetsContainerRoot
```

## Install

Import [pterodactyl/source-egg.json](pterodactyl/source-egg.json), choose the game **before first start**, and supply the relevant GSLT where required. For an unreleased dev build, use your locally built/published image instead of the export's `ghcr.io/edgegamers/source-egg:stable` placeholder. Channel images are usable after the release workflow succeeds.

Without a node, `SOURCE_MODE=auto` installs a private copy. With a node, it shares a fixed base-game generation and uses the central framework cache. Managed installations refuse unsafe local SteamCMD fallback when the node/mount is missing.

On a Linux Wings node (kernel 5.12+):

```bash
sudo bash scripts/install-node.sh --local ./dist/sourcenode
sudo sourcenode doctor
sudo sourcenode status
```

For published releases, omit `--local`. Edit `/etc/sourcenode/config.json` and restart the service to change policies. [Installation and operations](docs/install.md).

## Dev passwords

Set `GENERATE_PASSWORD=1` on participating dev servers. Node default `password_scope: "game"` gives CS:S servers one join password and TF2 servers another, separately on every node. Choose `server` for unique passwords or `node` for one across participating games. Admin-only `PASSWORD_SCOPE` can override the node default per server.

```bash
sudo sourcenode passwords show FULL_SERVER_UUID
```

RCON is independent. Ordinary webhooks never receive passwords; optional credential delivery uses `password_webhook`. [Password options and rotation](docs/install.md#passwords).

[Approved proposal](docs/source-egg-proposal.md) · [Feasibility research](docs/research/2026-10-24-source-engine-feasibility.md) · [Implementation plan](docs/superpowers/plans/2026-10-24-source-egg-implementation.md)

GPL-3.0-or-later. Adapted upstream helpers retain attribution; see [NOTICE](NOTICE).
