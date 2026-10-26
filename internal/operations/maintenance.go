package operations

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const UpgradesFile = "/etc/apt/apt.conf.d/52sourcenode-unattended-upgrades"
const UpgradesConfig = `// Written by sourcenode maintenance setup; delete to undo.
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
APT::Periodic::AutocleanInterval "7";
Unattended-Upgrade::Automatic-Reboot "false";
`

func (m *Manager) command(ctx context.Context, name string, args ...string) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if m.opt.Command == nil {
		return nil, errors.New("host command executor not configured")
	}
	b, e := m.opt.Command(ctx, name, args...)
	if len(b) > 1<<20 {
		return nil, errors.New("host command output exceeds bound")
	}
	return b, e
}

var journalSize = regexp.MustCompile(`take up ([0-9.]+)([BKMGT]?)`)
var reclaimedSize = regexp.MustCompile(`(?i)total reclaimed space:\s*([0-9.]+)\s*([kmgt]?i?b)`)

func reclaimed(b []byte) (items int, size int64) {
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "deleted:") {
			items++
		}
	}
	match := reclaimedSize.FindSubmatch(b)
	if match == nil {
		return items, 0
	}
	n, e := strconv.ParseFloat(string(match[1]), 64)
	if e != nil {
		return items, 0
	}
	unit := strings.ToLower(string(match[2]))
	factor := float64(1)
	if strings.Contains(unit, "i") {
		switch unit[0] {
		case 'k':
			factor = 1 << 10
		case 'm':
			factor = 1 << 20
		case 'g':
			factor = 1 << 30
		case 't':
			factor = 1 << 40
		}
	} else {
		switch unit[0] {
		case 'k':
			factor = 1e3
		case 'm':
			factor = 1e6
		case 'g':
			factor = 1e9
		case 't':
			factor = 1e12
		}
	}
	if n >= 0 && n*factor < float64(1<<62) {
		size = int64(n * factor)
	}
	return items, size
}

func (m *Manager) journalBytes(ctx context.Context) (int64, error) {
	b, e := m.command(ctx, "journalctl", "--disk-usage")
	if e != nil {
		return 0, e
	}
	v := journalSize.FindSubmatch(b)
	if v == nil {
		return 0, errors.New("unreadable journal disk usage")
	}
	n, e := strconv.ParseFloat(string(v[1]), 64)
	if e != nil {
		return 0, e
	}
	factor := map[string]float64{"": 1, "B": 1, "K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40}[string(v[2])]
	if n < 0 || n*factor > float64(1<<62) {
		return 0, errors.New("invalid journal size")
	}
	return int64(n * factor), nil
}

// Maintain performs an explicit pass even when scheduling is disabled. apply
// overrides dry_run, and is reserved for the privileged operator control path.
func (m *Manager) Maintain(ctx context.Context, apply bool) ([]TaskResult, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	c := m.cfg.Maintenance
	dry := c.DryRun && !apply
	var results []TaskResult
	var errs []error
	run := func(task string, fn func(*TaskResult) error) {
		r := TaskResult{Task: task, Dry: dry}
		e := fn(&r)
		if e != nil {
			r.Error = e.Error()
			errs = append(errs, e)
		}
		results = append(results, r)
	}
	if c.Images {
		run("images", func(r *TaskResult) error {
			if dry {
				b, e := m.command(ctx, "docker", "image", "ls", "--filter", "dangling=true", "--quiet")
				r.Items = len(strings.Fields(string(b)))
				r.Note = "would prune unused dangling images"
				return e
			}
			b, e := m.command(ctx, "docker", "image", "prune", "--force", "--filter", "dangling=true")
			r.Items, r.Bytes = reclaimed(b)
			return e
		})
	}
	if c.BuildCache {
		run("build-cache", func(r *TaskResult) error {
			r.Note = fmt.Sprintf("cache older than %dh", c.BuildCacheHours)
			if dry {
				return nil
			}
			b, e := m.command(ctx, "docker", "builder", "prune", "--force", "--filter", fmt.Sprintf("until=%dh", c.BuildCacheHours))
			r.Items, r.Bytes = reclaimed(b)
			return e
		})
	}
	if c.Journal {
		run("journal", func(r *TaskResult) error {
			before, e := m.journalBytes(ctx)
			if e != nil {
				return e
			}
			if dry {
				r.Note = fmt.Sprintf("journals occupy %d bytes; would keep %d days", before, c.JournalDays)
				return nil
			}
			if _, e = m.command(ctx, "journalctl", fmt.Sprintf("--vacuum-time=%dd", c.JournalDays)); e != nil {
				return e
			}
			after, e := m.journalBytes(ctx)
			if e != nil {
				return e
			}
			if after < before {
				r.Bytes = before - after
			}
			return nil
		})
	}
	m.mu.Lock()
	m.state.Maintenance = results
	if !dry {
		m.state.MaintenanceRuns++
		for _, r := range results {
			if r.Error == "" {
				m.state.MaintenanceBytes += r.Bytes
				total := m.state.MaintenanceTotals[r.Task]
				total.Runs++
				total.Items += int64(r.Items)
				total.Bytes += r.Bytes
				m.state.MaintenanceTotals[r.Task] = total
			}
		}
	}
	e := m.saveLocked()
	m.mu.Unlock()
	errs = append(errs, e)
	m.notice("maintenance.done", "", map[string]any{"dry_run": dry, "tasks": len(results)})
	return results, errors.Join(errs...)
}

// Setup is an explicit operator action. It is never called by Run or Tick.
// Host writes have no default implementation and must be deliberately injected.
func (m *Manager) Setup(ctx context.Context) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if m.opt.WriteHostFile == nil {
		return errors.New("explicit host file writer required")
	}
	b, e := m.command(ctx, "dpkg-query", "-W", "-f=${Status}", "unattended-upgrades")
	if e != nil && !strings.Contains(string(b), "no packages found") {
		return fmt.Errorf("unattended setup requires Debian or Ubuntu: %w", e)
	}
	if !strings.Contains(string(b), "install ok installed") {
		if _, e = m.command(ctx, "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "unattended-upgrades"); e != nil {
			return e
		}
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	return m.opt.WriteHostFile(ctx, UpgradesFile, []byte(UpgradesConfig), 0644)
}

func (m *Manager) Watch(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	disk, e := m.opt.Disk("/")
	if e != nil {
		return e
	}
	if disk.Total <= 0 || disk.Free < 0 {
		return errors.New("invalid disk sample")
	}
	reboot, e := m.opt.RebootRequired()
	if e != nil {
		return e
	}
	now := m.opt.Now()
	low := m.cfg.Maintenance.DiskWarnPercent > 0 && float64(disk.Free)/float64(disk.Total)*100 < float64(m.cfg.Maintenance.DiskWarnPercent)
	m.mu.Lock()
	previousDiskNotice, previousReboot := m.state.LowDiskNotice, m.state.RebootNotified
	diskNotice := low && (m.state.LowDiskNotice.IsZero() || now.Sub(m.state.LowDiskNotice) >= 24*time.Hour)
	rebootNotice := reboot && !m.state.RebootNotified
	if !low {
		m.state.LowDiskNotice = time.Time{}
	} else if diskNotice {
		m.state.LowDiskNotice = now
	}
	m.state.RebootNotified = reboot
	e = m.saveLocked()
	if e != nil {
		m.state.LowDiskNotice, m.state.RebootNotified = previousDiskNotice, previousReboot
	}
	m.mu.Unlock()
	if e != nil {
		return e
	}
	if diskNotice {
		m.notice("disk.low", "", map[string]any{"free": disk.Free, "total": disk.Total})
	}
	if rebootNotice {
		m.notice("reboot.required", "", nil)
	}
	return nil
}
