# Server controls

Set these variables in the server's panel Startup settings, then restart the server. The controls apply to CS:GO, CS:S, DOD:S, L4D2, and TF2. The egg reads custom arguments itself; keep the supplied panel startup command instead of inserting `{{CUSTOM_PARAMS}}` into a shell command.

| Panel variable | Default | Purpose |
| --- | --- | --- |
| `CUSTOM_PARAMS` | Empty | Additional game arguments, with quoted values supported. |
| `CLEANUP_ENABLED` | `0` | Enable the server's cleanup fallback before launch. |
| `ENABLE_FILTER` | `0` | Apply the server's console filter to panel output. |
| `CONSOLE_LOG_LEVEL` | Empty | Override the file's console level for this boot. |
| `GDB_DEBUG_PORT` | Empty | Run the game under `gdbserver` on the selected port. |
| `SOURCEMOD_PLUGIN_*` | `default` | Preserve, enable, or disable a supported stock SourceMod plugin. |
| `ENABLE_LOG_FILES` | `1` | Forward masked output to node console archives when available. Local rotating logs remain enabled. |

## Configuration files

Paths below are relative to the server volume, normally `/home/container` inside the container. Edit them through the panel file manager or your normal server file access.

| File | When it is read or created |
| --- | --- |
| `.source/configs/logging.json` | Every startup. |
| `.source/configs/console-filter.json` | Every startup, including when filtering is disabled. |
| `.source/configs/cleanup.json` | When local cleanup is enabled and the node has not taken ownership of cleanup. |

Missing files are created with defaults. Existing files are read without rewriting them, so custom settings and extra keys survive restarts and updates. Omitted supported fields use their defaults; a supplied array replaces the default array. These files do not use template-version migration or automatic rule merging.

Each file must be a regular JSON file of at most 256 KiB. Symlinked configuration files or directories are rejected. Malformed JSON and invalid effective settings stop startup with an error; the edited file is left intact. Correct the file and restart. A malformed console-filter file must be corrected even when `ENABLE_FILTER=0`.

## Custom startup arguments

For example, enter this as the value of `CUSTOM_PARAMS`:

```text
-tickrate 100 +sv_tags "community server" +motdfile 'my motd.txt'
```

The two quoted values each become one argument. The parser splits unquoted spaces and tabs, removes matching single or double quotes, and supports backslash escaping outside single quotes. It performs no shell expansion, variable substitution, glob expansion, redirection, or command substitution. Whether a particular game supports an argument remains the game's responsibility.

The input is limited to 8,192 bytes, 128 arguments, and 1,024 bytes per argument. Unclosed quotes, dangling escapes, newlines, NUL bytes, and the characters `;`, `$`, backtick, `|`, `&`, `<`, and `>` are rejected. A resulting argument cannot contain a literal double quote or backslash.

Managed settings must use their dedicated panel variables: `SOURCE_GAME`, `SERVER_PORT`, `SRCDS_MAP`, `MAX_PLAYERS`, `SERVER_HOSTNAME`, `SERVER_PASSWORD`, `RCON_PASSWORD`, and `STEAM_GSLT`. Custom arguments cannot replace the following settings or wrapper controls; matching is case-insensitive and also rejects `flag=value` forms:

```text
-game -port -ip +ip +hostport +map +hostname
+sv_password +rcon_password +sv_setsteamaccount
-maxplayers -maxplayers_override -console -usercon -norestart -strictportbind
-binary -debug -debugger -debuglog -steam -steamcmd_script
-steam_dir -steamcmd_dir -autoupdate -pidfile
```

With nonempty custom parameters, the egg launches the native engine executable directly, preserving argument boundaries that a shell wrapper could lose. It supplies the engine's library search paths and retains any existing `LD_LIBRARY_PATH` after those paths. With empty custom parameters and debugging disabled, the usual `srcds_run` or `srcds_run_64` launch remains in use.

## Console filtering and levels

The default `.source/configs/logging.json` is:

```json
{
  "logging": {
    "console_level": "INFO"
  }
}
```

Supported levels, from most verbose to least verbose, are `DEBUG`, `INFO`, `WARNING` (also `WARN`), and `ERROR`. Empty `CONSOLE_LOG_LEVEL` uses the file. A nonempty value overrides it for the current boot without changing the file.

Lines beginning with `[DEBUG]`, `[INFO]`, `[WARN]`, `[WARNING]`, or `[ERROR]`, or with `SourceEgg | LEVEL |`, use that severity. Other lines count as `INFO`. Consequently, `WARNING` or `ERROR` hides most untagged game output, including untagged messages containing the word "error". Empty lines pass through.

The default `.source/configs/console-filter.json` has no patterns:

```json
{
  "preview_mode": false,
  "patterns": []
}
```

Set `ENABLE_FILTER=1` and add patterns, for example:

```json
{
  "preview_mode": true,
  "patterns": [
    "Repeated diagnostic text",
    "@Server is hibernating"
  ]
}
```

Matching is case-sensitive. A pattern beginning with `@` matches the entire line after the `@`; other patterns match a substring. Patterns are plain text, not regular expressions. Up to 256 patterns are allowed, each at most 1,024 bytes. Empty patterns, a bare `@`, and patterns containing newline or NUL characters are rejected.

To try a filter, use `preview_mode: true` and set `CONSOLE_LOG_LEVEL=DEBUG`. Matching lines appear as `SourceEgg | DEBUG | Filter preview: ...`. Once the patterns are correct, set `preview_mode` to `false` to hide matches and restore your preferred level. Preview lines are hidden at levels above `DEBUG`.

Known credentials are masked before filtering. A pattern containing a raw password therefore does not match that password after it has become `[redacted]`.

Filtering and levels affect panel presentation only. Local `.source/logs/console.log` and node console delivery retain all masked lines, including filtered lines. Local rotation keeps the current log plus three archives (`console.1.log` through `console.3.log`), rotating at 32 MiB. `ENABLE_LOG_FILES=0` stops node forwarding; it does not disable local rotation.

The `SourceEgg | Ready` lifecycle message is always shown unchanged, including in preview mode. Wings needs it to mark startup complete.

## Standalone cleanup fallback

`CLEANUP_ENABLED=0` performs no local cleanup and does not read or create the cleanup file. Setting it to `1` enables one cleanup pass before the game starts, after installation and framework preparation. It does not start a periodic cleanup task.

The fallback also works for a server receiving other node services when node cleanup is disabled. When the node advertises that its cleanup module is enabled, the egg skips local cleanup and uses the node's policy. The node's rules are configured independently; the per-server cleanup file is not uploaded to the node. `CLEANUP_ENABLED` controls only this local fallback, not the node's cleanup module.

An older node may not advertise cleanup ownership. After negotiating the legacy protocol, the egg conservatively skips the local fallback to avoid two cleanup owners. Upgrade the node to use explicit ownership reporting.

The default `.source/configs/cleanup.json` is:

```json
{
  "on_start": true,
  "keep_open": true,
  "rules": [
    {
      "name": "demos",
      "directories": ["."],
      "patterns": ["*.dem"],
      "hours": 168
    },
    {
      "name": "logs",
      "directories": ["."],
      "patterns": ["L*.log", "errors_*.log"],
      "hours": 336
    },
    {
      "name": "dumps",
      "directories": ["."],
      "patterns": ["*.dmp", "*.mdmp"],
      "hours": 168
    },
    {
      "name": "cores",
      "directories": ["."],
      "patterns": ["core", "core.[0-9]*"],
      "hours": 24
    }
  ]
}
```

| Setting | Meaning |
| --- | --- |
| `on_start` | Defaults to `true`. `false` skips the local pass without removing the rules. |
| `keep_open` | Defaults to `true`. Keep files open by the egg process or its descendants; an unreadable process inventory fails the cleanup pass. |
| `rules` | Independent deletion rules. Use `[]` for no rules. Supplying a list replaces all four defaults. |
| Rule `name` | Required, nonempty, and unique within the file. |
| Rule `description` | Optional explanatory text. |
| Rule `directories` | Required nonempty list of paths relative to the server root. `.` means the root. |
| Rule `patterns` | Required nonempty list of filename globs such as `*.dem`; patterns cannot contain directory separators. |
| Rule `hours` | Files must be older than this many hours by modification time. `0` makes matching files immediately eligible. Range: 0–876,000. |
| Rule `recursive` | Defaults to `true` when omitted. `false` examines only the listed directories. |
| Rule `delete_parent_dir` | Defaults to `false`. For crash bundles, also remove a matched file's immediate parent and its regular-file siblings when safe. Never removes the rule's own directory. |
| Rule `enabled` | Defaults to `true` when omitted. `false` keeps the rule but skips it. |

Cleanup is confined to the server root and never follows symlinks. Absolute paths and paths escaping the root are rejected. Paths containing `.source`, `.sourcenode`, `.steam`, `Steam`, `steamapps`, or `steamcmd` components are protected, case-insensitively. Each pass has a 100,000-entry traversal budget.

Deletion is permanent. In particular, `delete_parent_dir: true` can remove sibling files that do not match the rule's pattern or age; use it only for dedicated crash-bundle directories. A bundle with a subdirectory, symlink, protected entry, or an open file is not removed as a whole. Cleanup reports file/byte totals and stores its statistics under `.source/cleanup/`. A runtime failure stops startup and may occur after earlier eligible files have already been deleted.

## Stock SourceMod plugins

These switches apply while SourceMod packages are deployed. Keep `INSTALL_SOURCEMOD=1` to install/update SourceMod and apply its switches; SourceMod also requires Metamod. Each switch accepts exactly `default`, `1`, or `0`:

| Value | Behavior |
| --- | --- |
| `default` | Preserve existing active/disabled placement, manual edits, and deleted defaults. A first installation uses the upstream package's placement. |
| `1` | Enable the bundled stock plugin. Can restore a previously deleted stock default from the package. |
| `0` | Disable the bundled stock plugin by placing it in the `disabled` directory. |

| Panel variable | Plugin file |
| --- | --- |
| `SOURCEMOD_PLUGIN_BASECOMMANDS` | `basecommands.smx` |
| `SOURCEMOD_PLUGIN_BASECHAT` | `basechat.smx` |
| `SOURCEMOD_PLUGIN_PLAYERCOMMANDS` | `playercommands.smx` |
| `SOURCEMOD_PLUGIN_ANTIFLOOD` | `antiflood.smx` |
| `SOURCEMOD_PLUGIN_ADMINHELP` | `adminhelp.smx` |
| `SOURCEMOD_PLUGIN_BASECOMM` | `basecomm.smx` |
| `SOURCEMOD_PLUGIN_BASEBANS` | `basebans.smx` |
| `SOURCEMOD_PLUGIN_RESERVEDSLOTS` | `reservedslots.smx` |
| `SOURCEMOD_PLUGIN_BASETRIGGERS` | `basetriggers.smx` |
| `SOURCEMOD_PLUGIN_FUNCOMMANDS` | `funcommands.smx` |
| `SOURCEMOD_PLUGIN_CLIENTPREFS` | `clientprefs.smx` |
| `SOURCEMOD_PLUGIN_ADMINMENU` | `adminmenu.smx` |
| `SOURCEMOD_PLUGIN_BASEVOTES` | `basevotes.smx` |
| `SOURCEMOD_PLUGIN_NOMINATIONS` | `nominations.smx` |
| `SOURCEMOD_PLUGIN_ROCKTHEVOTE` | `rockthevote.smx` |
| `SOURCEMOD_PLUGIN_NEXTMAP` | `nextmap.smx` |
| `SOURCEMOD_PLUGIN_FUNVOTES` | `funvotes.smx` |
| `SOURCEMOD_PLUGIN_MAPCHOOSER` | `mapchooser.smx` |

Active files live in `<game>/addons/sourcemod/plugins/`; disabled files live in its `disabled/` subdirectory. The game directories are `csgo`, `cstrike`, `dod`, `left4dead2`, and `tf`, respectively.

For a stock map-vote setup, enable `SOURCEMOD_PLUGIN_MAPCHOOSER=1` and `SOURCEMOD_PLUGIN_NEXTMAP=1`, together with `SOURCEMOD_PLUGIN_NOMINATIONS=1` and/or `SOURCEMOD_PLUGIN_ROCKTHEVOTE=1`. The switches do not automatically enable dependencies or replace a custom map-vote system. The dependency is defined by SourceMod's [nominations](https://github.com/alliedmodders/sourcemod/blob/master/plugins/nominations.sp) and [RTV](https://github.com/alliedmodders/sourcemod/blob/master/plugins/rockthevote.sp) plugins. There is no S2Script Zones equivalent in this egg.

Framework receipts identify stock file hashes. An unchanged stock plugin is updated in its current active or disabled location. Custom or edited plugin files are preserved. An explicit switch that would move a custom file fails startup with a refusal instead of moving or replacing it; manage that custom file yourself or return the switch to `default`. Duplicate active and disabled copies of the same plugin are an error and must be resolved before startup. Existing configuration/data files remain private and are not overwritten by framework upgrades.

## Remote debugging

To debug a server, allocate a separate port in the panel, set `GDB_DEBUG_PORT` to that port, and restart. Valid values are integers from 1 through 65,535, excluding `SERVER_PORT`. Empty disables debugging; `0` is not a valid panel value.

Use an updated runtime image built from this repository, which installs `gdbserver`. The launch uses the equivalent of:

```text
gdbserver --once 0.0.0.0:27016 ./srcds_linux64 <validated game arguments>
```

It launches a new engine process under the debugger; it does not attach to an existing server. `gdbserver` waits for a debugger connection, so normal game readiness will not occur until the debugger starts or continues the inferior. `--once` serves one debugger connection.

| Game | Native executable selection for debugging or custom argv |
| --- | --- |
| CS:GO, CS:S, DOD:S | `srcds_linux64` when present, otherwise `srcds_linux`. |
| L4D2 | `srcds_linux`. |
| TF2 | `srcds_linux64`. |

The selected file must exist and be executable. Missing binaries or `gdbserver` stop startup; the egg does not silently fall back to a different launch mode. The native engine receives the normal game arguments plus validated custom arguments, with library paths for the selected architecture.

The container must permit debugging through its ptrace/security policy, and the allocated TCP port must be reachable from the debugger. The egg does not change panel allocations, firewall rules, or container security settings. Debugger access controls the game process: restrict access to the debug port and clear `GDB_DEBUG_PORT` when finished. Install matching binaries/symbols in the debugging client as needed; the egg does not supply game debug symbols.

Native launches and argument handling are tested with temporary fake executables for all five profiles. The debugger wrapper is tested with a fake `gdbserver`. Acceptance against retail game binaries, a real debugger, and the runtime container remains a separate deployment check.
