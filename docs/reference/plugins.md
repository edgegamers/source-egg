# Per-server plugins

Server owners can manage third-party release packages and build SourcePawn plugins directly from repositories through the panel file manager and console. This works in standalone and node-managed servers; packages, credentials, toolchains, approvals and rollback files stay in the server's private volume. No node access is needed.

The manager checks at startup and every six hours by default. Manual manifests wait for approval of an exact package. Set `"auto_approve": true` to automatically queue successful downloads/builds for installation. Installs, updates, rollbacks and removals happen before the game launches during normal startup. Checks while the game runs only stage and queue packages; they never hot reload plugins or force a restart. Failed checks and builds keep installed files usable.

## Configuration

The first start creates `.source/plugins.json`:

```json
{
  "version": 1,
  "check_interval_hours": 6,
  "plugins": []
}
```

Add plugin objects to `plugins`. The examples below use placeholder repositories and asset names; replace them with the names and paths in your chosen release. Paths in `mappings.from` are relative to the package root; `mappings.to` and `preserve` are relative to the game directory, such as `cstrike`, `tf` or `csgo`. Do not include that game directory in the mapping.

This complete manifest demonstrates three different package layouts:

```json
{
  "version": 1,
  "check_interval_hours": 6,
  "plugins": [
    {
      "id": "match-tools",
      "source": {
        "provider": "github",
        "repository": "example-community/match-tools"
      },
      "asset": "match-tools-*-linux.zip",
      "format": "zip",
      "mappings": [
        {"from": "release/addons/sourcemod", "to": "addons/sourcemod"},
        {"from": "release/cfg", "to": "cfg"}
      ],
      "preserve": ["addons/sourcemod/gamedata/local-overrides.txt"],
      "prerelease": false
    },
    {
      "id": "private-stats",
      "source": {
        "provider": "gitlab",
        "url": "https://gitlab.example.net",
        "repository": "community/server-plugins/private-stats",
        "credential": "community-gitlab"
      },
      "asset": "private-stats-linux.tar.gz",
      "format": "tar.gz",
      "mappings": [
        {"from": ".", "to": "addons/sourcemod"}
      ]
    },
    {
      "id": "simple-chat",
      "source": {
        "provider": "github",
        "repository": "example-community/simple-chat"
      },
      "asset": "simple-chat.smx",
      "format": "raw",
      "mappings": [
        {"from": "simple-chat.smx", "to": "addons/sourcemod/plugins/simple-chat.smx"}
      ]
    }
  ]
}
```

The first ZIP has a wrapper directory:

```text
release/
  addons/sourcemod/
    plugins/match-tools.smx
    extensions/match-tools.ext.so
    gamedata/match-tools.txt
    translations/match-tools.phrases.txt
    configs/match-tools.cfg
  cfg/sourcemod/match-tools.cfg
```

The second archive starts inside the SourceMod directory, so its root maps to `addons/sourcemod`:

```text
plugins/private-stats.smx
gamedata/private-stats.txt
translations/private-stats.phrases.txt
configs/private-stats.cfg
```

The third release asset is a loose `simple-chat.smx`, with no archive. Its `from` is the exact asset filename and its `to` is the destination file. For archives, map a single exact file, a subtree or `.`. Mappings do not infer or strip wrapper directories.

`asset` accepts an exact filename or a glob, but it must match exactly one release asset. `format` accepts `zip`, `tar.gz`, `raw` or `auto`; omitting it uses extension detection. Stable releases are selected by default. Set `prerelease` to `true` to allow prereleases, or add `"pin": "v1.2.3"` for an exact release tag. GitLab prerelease filtering recognizes semver-style suffixes such as `v1.2.3-rc.1`; use a pin for repositories with other preview-tag conventions. A missing pinned tag does not fall back to latest. `check_interval_hours` accepts 1–8760; omitted or zero means six hours. A manifest supports up to 100 registered recipes. `source.url` overrides the provider base URL: a GitHub API base or a GitLab instance origin.

Mappings can place files below `addons`, `cfg`, `scripts`, `materials`, `models`, `sound`, `resource` and `maps`. A package can contain extensions, translations, gamedata and default configuration along with plugins. Missing sources, duplicate destinations, ambiguous assets, traversal, archive links, special files and oversized packages are rejected. Downloads are limited to 256 MiB, expanded archives to 512 MiB, individual files to 64 MiB and archive entries to 10,000. Ordinary ZIP archives are supported; ZIP64, multi-disk and self-extracting executable ZIP layouts are rejected. Release recipes do not compile source. No repository/install scripts run and runtime dependencies are not installed automatically.

## Automatic installation and pins

Add `"auto_approve": true` beside `version` in the existing manifest to cover
first installs and updates without per-version console approvals:

```json
{
  "version": 1,
  "check_interval_hours": 6,
  "auto_approve": true,
  "plugins": []
}
```

Keep your existing entries in `plugins`. Omitting `auto_approve` or setting it
to `false` retains manual approval. A plugin's own `"auto_approve": false` or
`true` overrides the manifest policy. `egg plugins list` shows the effective
`approval=auto` or `approval=manual` for each tracked entry.

The automatic flow is: check, download/build, verify the staged package, queue
it, and install before the game starts. Ready cached packages also qualify
after switching to automatic mode, including when the provider is offline.
Packages successfully prepared during startup can install on that launch.
Startup checks retain a shared 30-second budget; longer source builds retry
in the background and their completed packages wait for the next normal restart.
Only the newest successfully staged automatic snapshot is queued; a failed
newer check/build retains the previous valid package and installed files.

Version selection works independently of approval policy. For release assets,
use `pin` for an exact release tag. For source builds, use `source.ref` for a
branch, tag or full commit SHA. Pin every include dependency's `source.ref` to
a full commit too when reproducible source inputs are required. Changing a pin
selects a new version for the same automatic installation flow. Missing pins
never fall back to latest. Without exact pins, configured branches and release
channels continue tracking updates.

Automatic installs still block on unowned file collisions, framework ownership,
or locally edited managed code. They never imply `replace` or adoption. Fix the
conflict through the existing preview/keep/adopt/explicit-approval workflow.
Config, data, logs and other preserved paths retain their existing behavior.

Valid explicit pending installs, rollbacks and uninstalls take precedence for
that startup. A content recipe edit, including a changed pin, invalidates an
older approval and allows the newly selected automatic package to install.
Later automatic checks follow the configured source again: disable
automatic approval or pin the desired revision when holding a rollback, and
remove a recipe when uninstalling a tracked package permanently. Turning auto
off cancels automatic pending actions while retaining staging and explicit
approvals. `cancel` removes a queued action, but a later check can queue it again
while automatic mode remains enabled. Policy edits alone do not change package
IDs or invalidate explicit approvals.

## Build directly from a repository

Give each repository its own plugin entry. Adding `build` selects source mode:
SourceEgg resolves `source.ref` to an exact commit, downloads that commit's source
archive, and runs a pinned SourceMod compiler in private staging. No GitLab CI
pipeline or published plugin release is required. Both GitLab and public GitHub
repositories are supported. GitLab authentication uses the credentials described
below; use a token with `read_api` and access to each plugin/include repository.

For example, this entry compiles every `.sp` directly inside `scripting`, and
places the resulting plugins in the active directory:

```json
{
  "id": "dxlevel-restrict",
  "source": {
    "provider": "gitlab",
    "url": "https://gitlab.edgegamers.io",
    "repository": "source/plugins/counter-strike/dxlevel-restrict",
    "credential": "edgegamers-gitlab",
    "ref": "master"
  },
  "build": {
    "sourcemod": "1.13.0.7478",
    "sources": ["scripting/*.sp"]
  },
  "mappings": [
    {"from": "compiled", "to": "addons/sourcemod/plugins"}
  ]
}
```

This uses a repository path from the example CS:S Dockerfile; confirm its branch
and source layout before use. The [complete source manifest example](../examples/plugins-source.json)
also shows a disabled plugin, gamedata, and a private include dependency. Private
repositories require your credentials and have not been live-build validated.

Source recipes omit `asset`, `format`, `pin`, and `prerelease`. `source.ref`
defaults to `main`, and accepts a branch, tag or full commit ID. Use an exact
commit to freeze a source version. A build's version records its primary commit
and a digest covering the compiler tag and all resolved include commits, so an
include-only update can produce a new available build.

| Build field | Meaning |
| --- | --- |
| `sourcemod` | Required exact SourceMod release tag, such as `1.13.0.7478`. The compiler and standard includes come from its official Linux release archive. Choose a version compatible with the installed server; this does not upgrade its frameworks. Historical builds without a published GitHub release asset cannot be selected. |
| `sources` | Required 1–100 repository-relative `.sp` paths or glob patterns. Patterns are sorted, must match files, and can select at most 100 files in total. Globs do not recursively traverse directories. |
| `include_dirs` | Optional list of up to 100 include directories in the main repository. `.` is allowed. Each source's own directory and the toolchain's standard includes are searched too. |
| `dependencies` | Optional list of up to 20 include dependencies. Each has a `source` object and 1–100 `include_dirs` relative to that dependency's repository root. Dependencies do not compile or install runtime files. |

The provider's single archive wrapper directory is removed for source builds.
Every selected `path/name.sp` produces `compiled/name.smx`. Duplicate basenames,
missing inputs/includes, symlinks and an existing repository `compiled` path are
rejected. The `.smx` output must have SourcePawn binary magic and meet the normal
64 MiB file limit. Map `compiled/name.smx` to a specific destination to rename
it, or to `addons/sourcemod/plugins/disabled/name.smx` to keep it disabled. Map
gamedata, translations and config from their original repository-relative paths;
there is no implicit layout detection or asset installation.

Source checks have a five-minute budget per repository and each compiler process
has a one-minute limit. Startup retains its overall 30-second check budget;
timed-out source builds retry in the background scheduler after the game starts.
An unchanged, intact available build is reused. Build errors retain installed
files and earlier staged packages. Explicit approvals still bind exact bytes and
recipes; a later commit does not change an explicitly approved pending install.
Source packages use the same automatic/manual approval policy, normal-startup
installation, local-edit protection, rollback and removal commands as release
packages.

The manager executes only the compiler from the selected official toolchain,
with fixed arguments, a minimal environment, bounded diagnostics and named
credential redaction. This staging workspace is not an OS security sandbox:
compile source you trust. Git hooks, shell/build scripts, submodule fetching,
native extension compilation and automatic dependency solving are unsupported.
Source packages cannot overwrite framework-owned stock plugin filenames or
translations; use separately named resources/plugins with the stock equivalent
disabled, or maintain your existing custom framework separately.

## Private GitLab releases

Put server-specific tokens in `.source/plugin-credentials.json`, separately from the manifest:

```json
{
  "community-gitlab": "YOUR_GITLAB_TOKEN"
}
```

The `source.credential` value refers to a key in this object. Use a project or personal access token with `read_api` and permission to read the project's selected asset. GitLab deploy tokens do not authenticate the general release API; see [GitLab REST authentication](https://docs.gitlab.com/api/rest/authentication/). `source.url` is the GitLab instance origin; omit it for `https://gitlab.com`. Release links may point to job artifacts. Credentials are sent only to the configured origin, including its scheme and port, and are stripped on redirects to another origin. A private asset on another origin therefore needs a download link that works without forwarding this token.

Keep this file private in the panel; do not put tokens in console commands, repository URLs or the manifest. The manager does not put tokens in the game process environment or shared node cache. Installed game plugins run as the server user and can read server files, including this credentials file; install code you trust. GitHub acquisition supports public release assets.

## Check, review and approve manually

Enter commands in the panel console, one complete line at a time:

```text
egg plugins help
egg plugins list
egg plugins check
egg plugins check match-tools
egg plugins preview match-tools
egg plugins approve match-tools <snapshot-id>
```

These approval commands are optional in automatic mode. Replace `<snapshot-id>` with the full 64-character SHA-256 package ID printed by `preview`. Review every mapped path and any collision or modification notices. An explicit approval binds that exact staged content and recipe; it does not approve whatever upstream publishes later. `egg plugins update match-tools <snapshot-id>` is an alias for `approve`.

To cancel a queued install, rollback or uninstall before restarting, run `egg plugins cancel match-tools`. Cancellation retains the staged package and existing game files.

Restart through the panel when ready. The startup notice reports completion, and update notices name available and installed versions. All `SourceEgg | Plugins |` messages remain visible even when the console filter or log level hides ordinary lines. The masked messages also go to local logs and configured node console delivery.

Changing a mapping or other content recipe setting invalidates its earlier approval. Run `check`; automatic mode validates and queues the new package, while manual mode waits for preview/approval of the new exact ID. Approval policy changes alone retain package identities. Editing `.source/plugins.json` is supported while the game runs; checks and commands reload it. If malformed configuration prevents initialization, the game can still start: fix the file and issue a plugin command to retry. Restart to resume scheduled checks. An unrecoverable interrupted file transaction prevents startup until it is repaired.

You can add a recipe through the console instead of editing the manifest. The JSON object must fit on one line:

```text
egg plugins add {"id":"simple-chat","source":{"provider":"github","repository":"example-community/simple-chat"},"asset":"simple-chat.smx","format":"raw","mappings":[{"from":"simple-chat.smx","to":"addons/sourcemod/plugins/simple-chat.smx"}]}
```

All panel console users have permission to run these commands. The `egg` command namespace is consumed by the egg before it reaches the game terminal. RCON does not reach the manager. Commands are queued with a fixed bound; if the queue fills, a visible notice reports discarded commands so you can retry.

## Existing files and local edits

Framework receipts and other managed plugins own their paths. A plugin package cannot overwrite them. Existing unowned code also blocks installation. To bring an existing installation under management, stage the matching release, review it, then run:

```text
egg plugins adopt match-tools <snapshot-id>
```

Adoption verifies that the existing managed files match the staged package; it records ownership without replacing game files. Use the matching version or fix the recipe if adoption reports different files.

Existing files below `cfg`, `addons/sourcemod/configs`, `addons/sourcemod/data` and `addons/sourcemod/logs` are seed-only: packages can create missing defaults but updates, rollback and uninstall preserve existing contents. `addons/metamod.vdf` is protected too. `preserve` adds more file or subtree paths to this policy.

A locally modified managed file blocks replacement until you choose how to handle it. To retain a custom file:

```text
egg plugins keep match-tools addons/sourcemod/gamedata/match-tools.txt
egg plugins check match-tools
egg plugins preview match-tools
egg plugins approve match-tools <new-snapshot-id>
```

`keep` adds the path to the recipe's preserve list, so check and approve again. To intentionally replace modified managed code with the reviewed staged version, use `egg plugins approve match-tools <snapshot-id> replace`. This permission does not override another plugin's ownership, framework ownership or seed-only preservation.

## Rollback and removal

The manager retains two previous installed snapshots. `list` prints their exact IDs. Queue a rollback with:

```text
egg plugins list
egg plugins rollback match-tools <previous-snapshot-id>
```

The command previews changes and queues the selected snapshot for the next normal restart. Local modifications still require a keep decision or the explicit `replace` suffix. Rollback preserves configuration, data and logs; it does not restore old configuration over live edits.

Removing a plugin entry from `.source/plugins.json` stops tracking releases and cancels its pending install when startup validates the recipe. It does not delete plugin files. To remove owned files explicitly:

```text
egg plugins uninstall match-tools
egg plugins uninstall match-tools confirm
```

The first command previews eligible files. The second queues removal at the next restart. Only unchanged owned files are removed; local edits and preserved files remain. Confirmation saves the preview’s preservation choices, so removing the recipe afterward cannot expand the removal set. Later keep rules are also honored. Remove the manifest entry too if you no longer want update notices. Internal state, packages and recovery journals live under `.source/plugins/`; avoid editing these files manually.
