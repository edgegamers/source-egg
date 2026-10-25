# Build and test from source

Published installations use the [bootstrap and wizard](node.md); they do not require Go. For local development, use Linux amd64 and Go 1.27+:

```bash
bash scripts/verify.sh
docker build -t source-egg:dev .
bash scripts/smoke-container.sh source-egg:dev
```

The verification script runs tests/race checks, vet, formatting checks, Linux builds and shell syntax checks. Binaries are written under ignored `dist/`. The container smoke uses a fixture server with a read-only root and arbitrary UID; no retail game is downloaded.

The bootstrap tests need `jq` and util-linux (`setsid`, and `setpriv` when tests run as root). They use temporary fixtures and fake sudo/apt/download commands, without installing a node on the host:

```bash
bash scripts/test-install-node.sh
```

Optional external integration proofs:

```bash
SOURCE_EGG_UPSTREAM_TEST=1 go test -v ./internal/addons -run TestOfficialLinuxPackages
# In an isolated test environment with CAP_SYS_ADMIN:
SOURCE_EGG_MOUNT_TEST=1 go test -v ./internal/node -run TestReadOnlyMountTargetsContainerRoot
```

For local node installation on a Linux Wings host, run `sudo bash scripts/install-node.sh --local ./dist/sourcenode`. It uses the same wizard/config/service path as a release install. These commands must not be run against an unrelated live node merely to test the installer.

Release publishing is described in the [README](../README.md#publishing-a-release). The workflow attaches both `checksums.txt` and the legacy per-binary checksum so old/new bootstraps can install the same release.
