# Per-server plugins

Server owners can manage third-party plugin packages through the panel file manager and console. This works in standalone and node-managed servers; packages, credentials, approvals and rollback files stay in the server's private volume. No node access is needed.

The manager checks at startup and every six hours by default. It downloads and previews available versions, then waits for approval of an exact package. Approved installs, updates, rollbacks and removals happen before the game launches on the next normal restart. It never hot reloads plugins or forces a restart. Failed release checks keep the installed files usable.

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

Mappings can place files below `addons`, `cfg`, `scripts`, `materials`, `models`, `sound`, `resource` and `maps`. A package can contain extensions, translations, gamedata and default configuration along with plugins. Missing sources, duplicate destinations, ambiguous assets, traversal, archive links, special files and oversized packages are rejected. Downloads are limited to 256 MiB, expanded archives to 512 MiB, individual files to 64 MiB and archive entries to 10,000. Ordinary ZIP archives are supported; ZIP64, multi-disk and self-extracting executable ZIP layouts are rejected. No install scripts run, source code is not compiled, and dependencies are not installed automatically.

## Private GitLab releases

Put server-specific tokens in `.source/plugin-credentials.json`, separately from the manifest:

```json
{
  "community-gitlab": "YOUR_GITLAB_TOKEN"
}
```

The `source.credential` value refers to a key in this object. Use a project or personal access token with `read_api` and permission to read the project's selected asset. GitLab deploy tokens do not authenticate the general release API; see [GitLab REST authentication](https://docs.gitlab.com/api/rest/authentication/). `source.url` is the GitLab instance origin; omit it for `https://gitlab.com`. Release links may point to job artifacts. Credentials are sent only to the configured origin, including its scheme and port, and are stripped on redirects to another origin. A private asset on another origin therefore needs a download link that works without forwarding this token.

Keep this file private in the panel; do not put tokens in console commands, repository URLs or the manifest. The manager does not put tokens in the game process environment or shared node cache. Installed game plugins run as the server user and can read server files, including this credentials file; install code you trust. GitHub acquisition supports public release assets.

## Check, review and approve

Enter commands in the panel console, one complete line at a time:

```text
egg plugins help
egg plugins list
egg plugins check
egg plugins check match-tools
egg plugins preview match-tools
egg plugins approve match-tools <snapshot-id>
```

Replace `<snapshot-id>` with the full 64-character SHA-256 package ID printed by `preview`. Review every mapped path and any collision or modification notices. Approval binds that exact staged content and recipe; it does not approve whatever upstream publishes later. `egg plugins update match-tools <snapshot-id>` is an alias for `approve`.

To cancel a queued install, rollback or uninstall before restarting, run `egg plugins cancel match-tools`. Cancellation retains the staged package and existing game files.

Restart through the panel when ready. The startup notice reports completion, and update notices name available and installed versions. All `SourceEgg | Plugins |` messages remain visible even when the console filter or log level hides ordinary lines. The masked messages also go to local logs and configured node console delivery.

Changing a mapping or other recipe setting invalidates its earlier approval. Run `check`, inspect the new `preview`, and approve the new exact ID. When the managed path set changes, review the new preview before approving its exact package ID. Editing `.source/plugins.json` is supported while the game runs; checks and commands reload it. If malformed configuration prevents initialization, the game can still start: fix the file and issue a plugin command to retry. Restart to resume automatic checks. An unrecoverable interrupted file transaction prevents startup until it is repaired.

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
