package nodeinstall

import (
	"errors"
	"path/filepath"
	"slices"

	"github.com/edgegamers/source-egg/internal/node"
	"github.com/edgegamers/source-egg/internal/observability"
	"github.com/edgegamers/source-egg/internal/operations"
)

func configureOperations(c *node.Config, w *wizard) {
	w.sectionTitle("Discord alerts", "Ordinary notices never contain generated passwords. Use the separate private webhook for credentials.")
	hook := c.Observability.Alerts.Webhook
	if hook == "" {
		hook = c.Webhook
	}
	enabled := c.Observability.Alerts.Enabled && hook != ""
	w.yes(&enabled, "Enable Discord alerts")
	c.Observability.Alerts.Enabled = enabled
	if enabled {
		w.hook(&hook, "Discord event webhook")
		c.Webhook = hook
		c.Observability.Alerts.Webhook = hook
		if hook == "" {
			c.Observability.Alerts.Enabled = false
			w.ui.Help("No webhook configured; ordinary alerts remain disabled.")
		}
		w.list(&c.Observability.Alerts.Events, "Events to post", func(v []string) error {
			for _, s := range v {
				if s != "all" && !slices.Contains(observability.EventKinds, s) {
					return errors.New("Unknown event. Use all or one of the listed events.")
				}
			}
			return nil
		}, append([]string{"all = every ordinary notice."}, observability.EventKinds...)...)
		w.optional(&c.Observability.Alerts.Name, "Node name shown in messages", nil)
		w.text(&c.Observability.Alerts.Username, "Name the bot posts under", nil)
		w.optional(&c.Observability.Alerts.AvatarURL, "Avatar image URL", func(s string) error { x := observability.DefaultConfig(); x.Alerts.AvatarURL = s; return x.Validate() }, "HTTPS image URL; empty keeps the webhook picture.")
	}
	w.sectionTitle("Console archives", "One gzip-compressed archive per server per day on this node. Servers also need ENABLE_LOG_FILES=1.")
	w.yes(&c.Observability.Logs.Enabled, "Archive server console logs on this node")
	if c.Observability.Logs.Enabled {
		if c.Observability.Logs.Dir == "" {
			c.Observability.Logs.Dir = filepath.Join(c.StateDir, "logs")
		}
		w.path(&c.Observability.Logs.Dir, "Node console archive directory")
		w.number(&c.Observability.Logs.KeepDays, "Console archive retention days", 0, 365, "0 uses the hard 365-day ceiling. Older daily files are gzip-compressed.")
	}
	w.sectionTitle("Crash bundles", "Console tail, metadata and bounded nearby dump/log evidence.")
	w.yes(&c.Observability.Crashes.Enabled, "Collect crash bundles and nearby evidence")
	if c.Observability.Crashes.Enabled {
		if c.Observability.Crashes.Dir == "" {
			c.Observability.Crashes.Dir = filepath.Join(c.StateDir, "crashes")
		}
		w.path(&c.Observability.Crashes.Dir, "Crash bundle directory")
		w.number(&c.Observability.Crashes.KeepCount, "Crash bundles retained per server", 0, 10000, "0 = unlimited count.")
		w.number(&c.Observability.Crashes.KeepDays, "Crash bundle retention days", 0, 36500, "0 = unlimited age.")
	}
	w.sectionTitle("Backups", "Scheduled private server data, outside server volumes. Stock game files remain excluded.")
	w.yes(&c.Operations.Backup.Enabled, "Enable backups")
	if c.Operations.Backup.Enabled {
		b := &c.Operations.Backup
		if b.Dir == "" || b.Dir == "/var/lib/sourcenode/backups" {
			b.Dir = filepath.Join(c.StateDir, "backups")
		}
		w.path(&b.Dir, "Backup directory outside server volumes")
		w.number(&b.Hour, "Hour of the daily backup run", 0, 23, "Node local time, 0..23.")
		w.number(&b.KeepDays, "Days to keep a backup", 0, 36500, "0 = unlimited age.")
		w.number(&b.KeepCount, "Backups to keep per server", 0, 10000, "0 = unlimited count. Setting both limits to zero disables backup expiration.")
		validate := func(v []string) error { x := operations.DefaultConfig(); x.Backup.Include = v; return x.Validate() }
		w.list(&b.Include, "Paths to back up", validate, "Volume-relative paths/globs; all includes everything the exclusions leave.")
		w.list(&b.Exclude, "Paths to leave out", validate, "Patterns match paths, filenames and parent directories. Shared stock files are always left out.")
	}
	w.sectionTitle("Cleanup", "Choose when cleanup runs separately from how old each file category must be.")
	w.yes(&c.Operations.Cleanup.Enabled, "Enable cleanup")
	if c.Operations.Cleanup.Enabled {
		x := &c.Operations.Cleanup
		w.number(&x.EveryHours, "Hours between cleanup passes", 0, 8760, "0 disables periodic passes; on-start and manual cleanup remain available.")
		w.yes(&x.OnStart, "Also clean when a server starts")
		w.yes(&x.KeepOpen, "Leave files the server still has open", "Deleting an open file does not reclaim its disk space until the descriptor closes.")
		for i := range x.Rules {
			r := &x.Rules[i]
			enabled := r.Enabled == nil || *r.Enabled
			w.yes(&enabled, r.Name+": enabled", r.Description)
			if r.Enabled != nil || !enabled {
				r.Enabled = &enabled
			}
			if enabled {
				w.number(&r.Hours, r.Name+": retention hours", 0, 876000, "Only files older than this are removed; 0 removes eligible files on every pass.")
			}
		}
		w.ui.Help("Custom directories, patterns, recursion and parent-bundle rules are retained in config.json. Existing rules are never replaced by rerunning this wizard.")
	}
	w.sectionTitle("Metrics", "Resource usage, Prometheus endpoint and live terminal top.")
	w.yes(&c.Metrics.Enabled, "Enable metrics")
	if c.Metrics.Enabled {
		w.text(&c.Metrics.Listen, "Prometheus listener", func(s string) error { x := c.Metrics; x.Listen = s; return x.Validate() }, "127.0.0.1 keeps metrics local; use a public listener only with suitable firewall rules.")
		w.number(&c.Metrics.SampleSeconds, "Seconds between Docker samples", 1, 60)
	}
	w.sectionTitle("Host maintenance", "Docker and journal housekeeping; dry-run reports intended work. Security upgrades are configured separately.")
	w.yes(&c.Operations.Maintenance.Enabled, "Enable maintenance")
	if c.Operations.Maintenance.Enabled {
		m := &c.Operations.Maintenance
		w.number(&m.EveryHours, "Hours between maintenance passes", 0, 8760, "168 = weekly; 0 = manual only.")
		w.yes(&m.DryRun, "Keep host maintenance in dry-run mode")
		w.yes(&m.Images, "Prune dangling Docker images", "Tagged images and Docker volumes are never pruned.")
		w.yes(&m.BuildCache, "Prune Docker build cache")
		if m.BuildCache {
			w.number(&m.BuildCacheHours, "Hours of Docker build cache to retain", 1, 876000)
		}
		w.yes(&m.Journal, "Trim old journal files")
		if m.Journal {
			w.number(&m.JournalDays, "Days of journal to keep", 1, 36500)
		}
		w.number(&m.DiskWarnPercent, "Alert when free disk drops below this percent", 0, 100, "0 = no disk watch.")
	}
}
