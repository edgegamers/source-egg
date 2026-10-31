# Automatic plugin installs

The server owner chose automatic tracking of configured branches and releases
and explicitly asked to proceed without further design questions. Eliminate
routine exact-package approvals for opted-in first installs and updates.

## Approach

Add manifest-level `auto_approve: true`. Omitted/false retains manual approval
for existing manifests. An optional per-plugin boolean overrides the manifest.
Release `pin` and source/dependency `ref` keep their existing selection semantics;
exact version pins and full commit refs work with either approval policy.
Approval policy does not affect recipe hashes or staged package identities.

The alternatives were manual approval of every package, automatic tracking,
and automatic installation with all source/include revisions frozen. The owner
chose tracking; pinned operation remains supported through existing selectors.

## Lifecycle

Checks validate/build in staging and queue eligible automatic snapshots using
the existing installer preview with `replace=false`. Checks while the game is
running never write game files. New successful snapshots supersede previous
automatic pending installs; failed checks/builds preserve installed files and
previous valid staging. A currently installed matching revision clears stale
automatic pending installs.

At startup, collect ready cached snapshots and check for updates within the
existing 30-second budget, then apply pending actions before launching the game.
Ready packages can install on that startup, including cached packages when the
provider is unavailable. Source builds exceeding the startup budget retry in
the background and wait for the next normal restart.

Explicit manual install/rollback/uninstall actions take precedence for that
startup. Automatic policy does not supersede them during discovery. Future
automatic checks follow the configured source again; pin or disable automatic
approval when intentionally holding a rollback or uninstalling a tracked entry.
Disabling automatic approval cancels automatic pending actions, preserving
explicit approvals and staged packages. Cancel alone removes a queued action;
a later automatic check may queue it again.

## Boundaries

No forced restarts, hot installs, automatic replacement of local edits, automatic
adoption of unowned files, new pin commands, or changes to framework ownership.
File collisions and changed managed files block that automatic install with a
clear message. Config/data/log preservation and transaction recovery remain.
List output identifies the effective approval policy. Update reference docs,
examples and this server's generated manifests to opt into automatic tracking.

## Validation

Exercise first startup, offline existing staging, source builds and failures,
background queueing, per-plugin overrides, policy opt-out, explicit actions,
local edit conflicts, pinned release selection and unchanged package hashes.
Run the full race-test suite, vet, builds and a fresh whole-branch review.
