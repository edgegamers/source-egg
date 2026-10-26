# Node configuration

Configuration is `/etc/sourcenode/config.json`. Rerun `sudo sourcenode install` for the wizard; advanced edits take effect after `sudo systemctl restart sourcenode`. Saved choices are preserved on reruns. Private configuration and password state are not exposed through the shared mounts.

The following is the **fresh-install** shape generated from `node.FreshConfig()`. The example node name stands for the host's actual hostname. Empty log/crash paths resolve beneath `state_dir`; the default backup path follows a customized `state_dir`. A blank self-update channel follows `node_channel`. Empty webhook URLs, Steam credentials and optional password paths/cron are omitted.

```json
{
  "password_enabled": false,
  "password_timezone": "UTC",
  "push_method": "symlink",
  "max_workers": 8,
  "auto_restart": true,
  "steam_validate": false,
  "operations": {
    "backup": {
      "enabled": true,
      "dir": "/var/lib/sourcenode/backups",
      "hour": 5,
      "keep_days": 14,
      "keep_count": 7,
      "include": [
        "all"
      ],
      "exclude": [
        "*.vpk",
        "*.dem",
        "steamapps",
        "Steam",
        "steamcmd",
        ".steam",
        "temps",
        "egg/cache"
      ],
      "min_free_bytes": 1073741824,
      "max_bytes": 107374182400,
      "max_files": 100000
    },
    "cleanup": {
      "enabled": true,
      "every_hours": 6,
      "on_start": true,
      "keep_open": true,
      "rules": [
        {
          "name": "demos",
          "directories": [
            "."
          ],
          "patterns": [
            "*.dem"
          ],
          "hours": 168
        },
        {
          "name": "logs",
          "directories": [
            "."
          ],
          "patterns": [
            "L*.log",
            "errors_*.log"
          ],
          "hours": 336
        },
        {
          "name": "dumps",
          "directories": [
            "."
          ],
          "patterns": [
            "*.dmp",
            "*.mdmp"
          ],
          "hours": 168
        },
        {
          "name": "cores",
          "directories": [
            "."
          ],
          "patterns": [
            "core",
            "core.[0-9]*"
          ],
          "hours": 24
        }
      ]
    },
    "maintenance": {
      "enabled": true,
      "every_hours": 168,
      "dry_run": true,
      "images": true,
      "build_cache": true,
      "build_cache_hours": 168,
      "journal": true,
      "journal_days": 14,
      "disk_warn_percent": 15
    }
  },
  "observability": {
    "alerts": {
      "enabled": true,
      "events": [
        "all"
      ],
      "name": "example-node",
      "username": "sourcenode",
      "queue_size": 256
    },
    "logs": {
      "enabled": true,
      "dir": "",
      "keep_days": 30,
      "queue_size": 2048,
      "max_daily_bytes": 268435456
    },
    "crashes": {
      "enabled": true,
      "dir": "",
      "keep_count": 20,
      "keep_days": 30,
      "queue_size": 8,
      "max_bundle_bytes": 1073741824
    }
  },
  "self_update": {
    "enabled": true,
    "channel": "",
    "check_minutes": 1440
  },
  "password_format": "eff",
  "password_calendar_months": 1,
  "password_words": 0,
  "metrics": {
    "enabled": true,
    "listen": "127.0.0.1:9151",
    "sample_seconds": 10
  },
  "node_channel": "stable",
  "prefetch_games": null,
  "state_dir": "/var/lib/sourcenode",
  "shared_dir": "/srv/source-shared",
  "cache_dir": "/var/cache/sourcenode",
  "steamcmd": "/opt/steamcmd/steamcmd.sh",
  "images": [
    "ghcr.io/edgegamers/source-egg"
  ],
  "shared": true,
  "framework_cache": true,
  "framework_pins_enabled": true,
  "framework_refresh_minutes": 60,
  "framework_keep_days": 30,
  "poll_seconds": 5,
  "check_minutes": 1,
  "restart": {
    "mode": "immediate",
    "window": "04:00-06:00",
    "max_delay_minutes": 720,
    "warn_seconds": 300
  },
  "password_scope": "server",
  "password_interval_hours": 0,
  "wings_config": ""
}
```

## Existing configurations

`LoadConfig` fills omitted keys with legacy defaults instead of fresh-install choices. This keeps upgrades from enabling backups, cleanup, maintenance, metrics, console archiving or automatic node upgrades that were previously off. Existing crash retention remains ten bundles, game checks remain thirty minutes, and restart policy remains empty-server with the existing deadline unless saved settings say otherwise. A missing password master switch retains prior enabled behavior, per-game scope, token format and fixed-hour schedule. Explicit `false`, `0`, custom rules and saved credentials are retained.

New installations use the enabled modules shown above, twenty crash bundles, ten-second metrics sampling, daily self-update checks and immediate game-update restarts. Passwords remain disabled until selected; the offered policy is per-server EFF phrases, three words, monthly rotation.

## Game files and updates

`push_method` is `symlink`, `hardlink` or `copy`. Symlink mode uses the read-only shared mount and saves the most space. Hardlinks require the same filesystem, non-writable shared VPKs and a stable non-root server process identity distinct from the inode owner. Unsupported hardlinks fail explicitly. Copies consume per-server disk space. Hardlink/copy modes require a matched image that advertises host-delivery support; an older image receives an upgrade error. All modes preserve private server configs/addons and immutable generations.

`max_workers` bounds concurrent server preparation (1–256, default 8). `steam_validate` adds `validate` to node SteamCMD downloads. `auto_restart: false` keeps running servers on their current generation after a game update; a later manual start applies the selected generation. It does not disable explicit password-change restarts.

Restart `mode` is `immediate`, `empty` or `window`. A window permits a restart when the server is empty **or** the node-local `HH:MM-HH:MM` window is open. Windows may cross midnight; equal endpoints are invalid. A positive `max_delay_minutes` can override waiting, while zero removes that deadline. Unknown player counts are never treated as empty and wait for a window/deadline. Countdown uses `warn_seconds`.

## Password schedules

Optional keys omitted above:

| Key | Default / meaning |
|---|---|
| `password_cron` | Empty; standard five-field schedule when set |
| `password_timezone` | `UTC`; IANA zone for cron, such as `America/Chicago` |
| `password_dir` | Empty; private `state_dir/passwords` |
| `password_wordlist` | Empty; embedded EFF long word list |
| `password_webhook` | Empty; separate private Discord credential delivery |

For example, rotate at 04:00 every Monday in Chicago:

```json
{
  "password_enabled": true,
  "password_scope": "server",
  "password_format": "eff",
  "password_words": 3,
  "password_cron": "0 4 * * MON",
  "password_timezone": "America/Chicago",
  "password_interval_hours": 0,
  "password_calendar_months": 0
}
```

Fields are minute, hour, day of month, month and weekday. Lists, ranges, steps, month/weekday names and Sunday 0/7 are supported. Examples: `0 4 * * *` daily, `30 3 * * MON-FRI` weekdays, `0 */6 * * *` every six hours. Seconds, `@daily` macros and embedded timezone prefixes are not accepted. The wizard validates the expression and previews the next new-password rotation.

Day matching follows Cronie: restricted day-of-month and weekday fields are ORed; when either starts with `*` (including `*/n`), both conditions must match. A skipped local time during spring DST does not run; both occurrences of a repeated fall time can run. Times are persisted as UTC. A node returning after downtime performs one overdue rotation when the assignment is requested, then schedules the next future occurrence.

A nonempty cron overrides the legacy interval fields; otherwise a positive `password_calendar_months` overrides `password_interval_hours`. The wizard clears unused schedule fields. Elapsed intervals remain elapsed time; calendar months use UTC and clamp month-end dates. All-zero intervals with no cron mean no expiry.

Saved passwords retain their stored due dates until their next ordinary rotation; changing schedule/format does not immediately invalidate credentials. Previously non-expiring assignments remain non-expiring. `password_enabled: false` stops issuing new assignments and rotations; enrolled servers retain their cached password. A server without a cached assignment fails closed if generation is requested while the module is off. Per-server opt-in remains `GENERATE_PASSWORD=1`; `PASSWORD_SCOPE=default` follows node policy.

Custom dictionaries accept one word per line or EFF dice-number/word lines. They need at least 7,772 distinct usable lowercase ASCII words, each 2–16 letters, and at most 4 MiB. The four hyphenated EFF compounds are excluded from dice-format input. Dictionaries are loaded on daemon startup. Stop the daemon and move the existing private state before changing `password_dir` if assignments must be retained; changing this path alone does not migrate files.

## Retention and operational modules

All JSON durations are explicit minutes/hours/days. Backup `keep_days` and `keep_count` each accept zero for unlimited; both zero disable expiration. Crash retention accepts zero for unlimited age/count, subject to existing rate/size bounds. Console retention zero uses the hard 365-day ceiling. Node console archives additionally require server `ENABLE_LOG_FILES=1`.

Cleanup rule `directories` and backup patterns are server-volume-relative. Cleanup `recursive`/`enabled` default true when omitted. Rule `hours: 0` makes every eligible matching file old enough; `every_hours: 0` disables only periodic passes. `on_start` and `keep_open` are independent switches. `delete_parent_dir` removes only a bounded matching leaf bundle, never the rule root or a directory holding subdirectories. The wizard retains custom directories, patterns and recursion when editing rule switches and ages.

`framework_refresh_minutes` controls metadata caching independently of game checks. `framework_keep_days: 0` disables unused-archive expiry; installed/pinned/unknown legacy archives stay protected. Framework policy persists in private `framework-pins.json`; the wizard changes only reviewed fleet pins and retains server exceptions. Panel exact pins take precedence. Game-generation pins remain separate in `pins.json`.

Host maintenance starts in dry-run mode; `maintenance now --apply` applies one pass. Image, build-cache and journal tasks, their retention and the disk warning threshold are independently configurable. Unattended security upgrades require the explicit setup command and never enable automatic reboot. Metrics defaults to loopback. Node self-update uses `stable`, `beta` or manual-only `dev`; it does not replace server images.

## Notices and storage

`observability.alerts.webhook` overrides legacy top-level `webhook`; an empty override inherits it. An empty effective webhook or `enabled: false` disables ordinary notices. `password_webhook` is independent. Events use `["all"]` or a list from the operations guide. `username`, `name` and HTTPS `avatar_url` customize ordinary posts.

Private state, backups, logs, crashes and passwords must stay outside public shared/cache roots. Use dedicated real directories. Secrets are hidden in wizard summaries; explicit root `passwords show SERVER` is the credential reveal operation.

[Operations and commands](../operations.md) · [Install guide](../node.md) · [Server controls](../server-controls.md)
