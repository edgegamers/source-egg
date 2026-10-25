# Reference Installation Flow Implementation Plan

> Use the execution and test-first skills in this chat. The user requested the reference README and egg/node install experience; proceed within that scope.

**Goal:** Present Source Egg like the reference: optional node first, one curl-to-bash bootstrap, verified release downloads, an interactive Go setup wizard, then egg import and game/framework choices. Preserve the approved Source-only scope.

**Architecture:** Keep the existing game runtime. Add a node installation package for configuration questions, atomic config/binary/unit installation and service restart. The bootstrap handles release selection, checksum verification, sudo/dependencies and terminal handoff. Optional configured game prefetch supports node setup before servers exist. Rerun the bootstrap to update the node binary; do not advertise an unimplemented automatic updater.

**Files:** `internal/nodeinstall/{wizard,install}.go` and tests; `cmd/sourcenode/main.go`; `internal/node/{config,daemon,warmup}.go` and tests; `scripts/{install-node,test-install-node}.sh`; README and node/install/building docs; release/CI workflows.

## Tasks

- [x] Write failing tests for interactive scope/feature choices, existing-config preservation, interrupted setup, private atomic config save and service restart failure; implement the Go installer and `sourcenode install` command.
- [x] Write failing tests for selected-game prefetch and invalid profiles; implement configured warmup without server registration.
- [x] Write/run bootstrap fixtures covering curl-pipe headless install, stable/beta/exact release selection, checksum tampering, incomplete assets and argument validation; replace the root-only bootstrap with sudo/TTY-aware handoff.
- [x] Rewrite README around the reference's three-step install structure; separate node, server and source-build guides; document real capabilities and remaining validation honestly.
- [x] Run race/vet/build/shell/bootstrap checks and independent review; address material findings and leave changes reviewable on the implementation branch.

## Constraints

- No Workshop/map caching or connection guard.
- Password policy remains game/server/node, opt in per server, default per-game dev policy.
- Existing config survives headless reruns and upgrades. Only explicit wizard answers/channel flags change settings.
- Missing/incomplete releases and bad checksums fail before executing downloaded code.
- Do not run the real installer against the Windows host or change connected live game servers.
- Previous push authorization was completed; this follow-up prepares repository changes without publishing a release or altering a live node.

## Execution record

Independent read-only review found a root-directory permission hazard via cleaned/symlinked managed paths. `TestUnsafeManagedDirectoriesFailBeforeProvisioning` reproduced the failure without allowing filesystem mutations; the installer now rejects protected roots, symlinked paths and private-state placement inside public stores before provisioning. Existing config-parent permissions are preserved by a separate regression. No real host service was installed.

Deferred minor: existing SteamCMD installations are reused rather than comprehensively repaired; a damaged existing client may need manual repair. This is documented in the node guide. Automatic node self-updates remain outside this install-flow change; rerunning the verified bootstrap upgrades the binary.

Final verification: Linux `scripts/verify.sh` passed (tests with race detection, vet, formatting, binary builds and shell syntax). `scripts/test-install-node.sh` passed with real jq and isolated fake host commands, including legacy checksum compatibility. The production image built, and `scripts/smoke-container.sh source-egg:dev` passed under a read-only root and arbitrary UID with a fixture server. Documentation links and `git diff --check` passed. Live game downloads/joins and real systemd installation remain acceptance work; no live host was modified.
