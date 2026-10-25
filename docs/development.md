# Build and test from source

Published installations use the [bootstrap and wizard](node.md); they do not require Go. For local development, use Linux amd64 and Go 1.27+:

```bash
bash scripts/verify.sh
docker build -t source-egg:dev .
bash scripts/smoke-container.sh source-egg:dev
```

The verification script runs tests/race checks, vet, formatting checks, Linux builds and shell syntax checks. Binaries are written under ignored `dist/`. The container smoke uses a fixture server with a read-only root and arbitrary UID; no retail game is downloaded.

Image builds download Valve's current SteamCMD packages over HTTPS and verify their SHA-256 hashes against Valve's manifest. They do not execute the legacy tarball's self-updater, which can fail to reach its update servers on hosted CI runners. The seed includes both Steam client library architectures; normal SteamCMD checks still run when installing/updating a game. `scripts/test-seed-steamcmd.sh` checks the download/checksum flow with isolated fixtures and an updater that would fail if executed.

The bootstrap tests need `jq` and util-linux (`setsid`, and `setpriv` when tests run as root). They use temporary fixtures and fake sudo/apt/download commands, without installing a node on the host:

```bash
bash scripts/test-install-node.sh
```

Operational package tests inject clocks, HTTP and command execution and use private temporary volumes. They do not restore a real server, prune Docker state, write apt configuration or restart host services. Full Linux race tests include observability rollover/recovery, root confinement and safe update/uninstall behavior. The EFF dictionary is embedded with attribution and pinned LF bytes.

Optional external integration proofs:

```bash
SOURCE_EGG_UPSTREAM_TEST=1 go test -v ./internal/addons -run TestOfficialLinuxPackages
# In an isolated test environment with CAP_SYS_ADMIN:
SOURCE_EGG_MOUNT_TEST=1 go test -v ./internal/node -run TestReadOnlyMountTargetsContainerRoot
```

For local node installation on a Linux Wings host, run `sudo bash scripts/install-node.sh --local ./dist/sourcenode`. It uses the same wizard/config/service path as a release install. These commands must not be run against an unrelated live node merely to test the installer.

Release publishing is described in the [README](../README.md#publishing-a-release). The workflow attaches both `checksums.txt` and the legacy per-binary checksum so old/new bootstraps can install the same release.
