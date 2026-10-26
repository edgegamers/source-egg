package nodeinstall

import (
	"fmt"
	"path/filepath"

	"github.com/edgegamers/source-egg/internal/node"
)

func configureOperations(c node.Config, w wizard) (node.Config, error) {
	var err error
	fmt.Fprintln(w.output, "Optional operations modules (existing choices are retained)")
	if c.PasswordFormat, err = w.ask("Join-password format (token or eff word phrase)", c.PasswordFormat, func(s string) error {
		if s != "token" && s != "eff" {
			return fmt.Errorf("choose token or eff")
		}
		return nil
	}); err != nil {
		return c, err
	}
	if c.PasswordFormat == "eff" {
		words := c.PasswordWords
		if words == 0 {
			words = 3
		}
		if c.PasswordWords, err = w.number("Words per join-password phrase", words, 3, 16); err != nil {
			return c, err
		}
	}
	if c.PasswordMonths, err = w.number("Calendar months per rotation (0 uses hours above)", c.PasswordMonths, 0, 1200); err != nil {
		return c, err
	}
	if c.Observability.Logs.Enabled, err = w.boolean("Archive server console logs on this node", c.Observability.Logs.Enabled); err != nil {
		return c, err
	}
	if c.Observability.Logs.Enabled {
		dir := c.Observability.Logs.Dir
		if dir == "" {
			dir = filepath.Join(c.StateDir, "logs")
		}
		if c.Observability.Logs.Dir, err = w.ask("Node console archive directory", dir, nil); err != nil {
			return c, err
		}
		if c.Observability.Logs.KeepDays, err = w.number("Console archive retention days (hard ceiling 365)", c.Observability.Logs.KeepDays, 0, 365); err != nil {
			return c, err
		}
	}
	if c.Observability.Crashes.Enabled, err = w.boolean("Collect crash bundles and nearby evidence", c.Observability.Crashes.Enabled); err != nil {
		return c, err
	}
	if c.Observability.Crashes.Enabled {
		dir := c.Observability.Crashes.Dir
		if dir == "" {
			dir = filepath.Join(c.StateDir, "crashes")
		}
		if c.Observability.Crashes.Dir, err = w.ask("Crash bundle directory", dir, nil); err != nil {
			return c, err
		}
		if c.Observability.Crashes.KeepDays, err = w.number("Crash bundle retention days (0 no age limit)", c.Observability.Crashes.KeepDays, 0, 3650); err != nil {
			return c, err
		}
		if c.Observability.Crashes.KeepCount, err = w.number("Crash bundles retained per server (0 no count limit)", c.Observability.Crashes.KeepCount, 0, 10000); err != nil {
			return c, err
		}
	}
	if c.Operations.Backup.Enabled, err = w.boolean("Schedule nightly private-data backups", c.Operations.Backup.Enabled); err != nil {
		return c, err
	}
	if c.Operations.Backup.Enabled {
		dir := c.Operations.Backup.Dir
		if dir == "" || dir == "/var/lib/sourcenode/backups" {
			dir = filepath.Join(c.StateDir, "backups")
		}
		if c.Operations.Backup.Dir, err = w.ask("Backup directory outside server volumes", dir, nil); err != nil {
			return c, err
		}
		if c.Operations.Backup.Hour, err = w.number("Nightly backup hour (node local time)", c.Operations.Backup.Hour, 0, 23); err != nil {
			return c, err
		}
		if c.Operations.Backup.KeepDays, err = w.number("Backup retention days", c.Operations.Backup.KeepDays, 1, 3650); err != nil {
			return c, err
		}
		if c.Operations.Backup.KeepCount, err = w.number("Backups retained per server", c.Operations.Backup.KeepCount, 1, 10000); err != nil {
			return c, err
		}
	}
	if c.Operations.Cleanup.Enabled, err = w.boolean("Schedule server demo/log/dump cleanup", c.Operations.Cleanup.Enabled); err != nil {
		return c, err
	}
	if c.Operations.Cleanup.Enabled {
		if c.Operations.Cleanup.EveryHours, err = w.number("Cleanup interval hours (0 only on start)", c.Operations.Cleanup.EveryHours, 0, 8760); err != nil {
			return c, err
		}
	}
	if c.Metrics.Enabled, err = w.boolean("Collect resource metrics and enable Prometheus", c.Metrics.Enabled); err != nil {
		return c, err
	}
	if c.Metrics.Enabled {
		if c.Metrics.Listen, err = w.ask("Prometheus listener (loopback recommended)", c.Metrics.Listen, func(s string) error { m := c.Metrics; m.Listen = s; return m.Validate() }); err != nil {
			return c, err
		}
	}
	if c.Operations.Maintenance.Enabled, err = w.boolean("Schedule optional host maintenance/disk watches", c.Operations.Maintenance.Enabled); err != nil {
		return c, err
	}
	if c.Operations.Maintenance.Enabled {
		if c.Operations.Maintenance.DryRun, err = w.boolean("Keep host maintenance in dry-run mode", c.Operations.Maintenance.DryRun); err != nil {
			return c, err
		}
	}
	if c.SelfUpdate.Enabled, err = w.boolean("Automatically update the node binary", c.SelfUpdate.Enabled); err != nil {
		return c, err
	}
	if c.SelfUpdate.Enabled {
		if c.SelfUpdate.CheckMinutes, err = w.number("Node update check interval minutes", c.SelfUpdate.CheckMinutes, 1, 10080); err != nil {
			return c, err
		}
	}
	return c, c.Validate()
}
