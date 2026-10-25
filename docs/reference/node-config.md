# Node configuration

Configuration is `/etc/sourcenode/config.json`. The installer merges missing keys with typed defaults and preserves saved choices. Rerun `sudo sourcenode install` for the wizard; advanced edits take effect after `sudo systemctl restart sourcenode`.

This is the complete default shape generated from the implementation. Empty log/crash paths resolve beneath `state_dir`; the default backup path also follows a customized `state_dir`. A blank self-update channel follows `node_channel`. Webhook URLs and Steam credentials are omitted when empty.

```json
{
  "operations": {
    "backup": {
      "enabled": false,
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
      "enabled": false,
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
      "enabled": false,
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
      "username": "sourcenode",
      "queue_size": 256
    },
    "logs": {
      "enabled": false,
      "dir": "",
      "keep_days": 30,
      "queue_size": 2048,
      "max_daily_bytes": 268435456
    },
    "crashes": {
      "enabled": true,
      "dir": "",
      "keep_count": 10,
      "keep_days": 30,
      "queue_size": 8,
      "max_bundle_bytes": 1073741824
    }
  },
  "self_update": {
    "enabled": false,
    "channel": "",
    "check_minutes": 360
  },
  "password_format": "token",
  "password_calendar_months": 0,
  "password_words": 0,
  "metrics": {
    "enabled": false,
    "listen": "127.0.0.1:9151",
    "sample_seconds": 1
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
  "check_minutes": 30,
  "restart": {
    "mode": "empty",
    "window": "",
    "max_delay_minutes": 60,
    "warn_seconds": 60
  },
  "password_scope": "game",
  "password_interval_hours": 168,
  "wings_config": ""
}
```

`observability.alerts.webhook` overrides the legacy top-level `webhook`; an empty override inherits it. An empty effective webhook disables ordinary notices. Set `observability.alerts.enabled` false to disable them explicitly. `password_webhook` remains independent and private. Ordinary event selection is `events: ["all"]` or a list from the operations guide. `username`, `name` and HTTPS `avatar_url` customize posts.

New optional modules keep their `enabled: false` defaults on existing configurations. Crash bundles remain enabled; ordinary alerts become active only with a configured webhook. Server console archives additionally require `ENABLE_LOG_FILES=1`. Changing paths must keep private state, backups, logs and crashes outside the public shared/cache mounts.

All time durations in JSON are explicit minutes/hours/days rather than nanoseconds. `password_calendar_months > 0` overrides `password_interval_hours`; format `token` retains current behavior, while `eff` selects the bundled EFF dictionary and defaults to three words. Scope remains `game`, `server` or `node`. Existing assignments retain their saved next-rotation date.

Cleanup rule `directories` and backup patterns are relative to a server volume. Cleanup `recursive`/`enabled` default true when omitted. `delete_parent_dir` removes only a bounded matching leaf bundle, never the rule root or a directory holding subdirectories. Server-owned configs, plugins, event assets and open logs are not blanket cleanup targets.

Backup `keep_days` and `keep_count` cannot both be zero. Crash retention may use zero for no age/count limit, subject to its rate and size bounds. Console retention always has a 365-day ceiling. Framework archive retention uses `framework_keep_days`; zero disables that sweep, and installed/pinned/unknown legacy archives are protected. `framework_refresh_minutes` controls metadata caching independently of game polling.

Host maintenance remains dry-run until changed explicitly; `maintenance now --apply` applies one pass. Configuring unattended upgrades is a separate operator action and never enables automatic reboot. Metrics defaults to loopback. Node self-update uses the selected stable/beta channel or exact configured release; it does not update server images.

Framework CLI policy persists separately in private `framework-pins.json`; game generation pins remain in `pins.json`. Operator commands update these files through the running daemon instead of editing a stale in-memory config.

[Operations and commands](../operations.md) · [Install guide](../node.md)
