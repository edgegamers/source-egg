// Package operations provides optional root-confined server file operations and
// explicitly injected host maintenance. Server discovery remains with the node.
package operations

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Backup      BackupConfig      `json:"backup"`
	Cleanup     CleanupConfig     `json:"cleanup"`
	Maintenance MaintenanceConfig `json:"maintenance"`
}
type BackupConfig struct {
	Enabled      bool     `json:"enabled"`
	Dir          string   `json:"dir"`
	Hour         int      `json:"hour"`
	KeepDays     int      `json:"keep_days"`
	KeepCount    int      `json:"keep_count"`
	Include      []string `json:"include"`
	Exclude      []string `json:"exclude"`
	MinFreeBytes int64    `json:"min_free_bytes"`
	MaxBytes     int64    `json:"max_bytes"`
	MaxFiles     int      `json:"max_files"`
}
type CleanupConfig struct {
	Enabled    bool   `json:"enabled"`
	EveryHours int    `json:"every_hours"`
	OnStart    bool   `json:"on_start"`
	KeepOpen   bool   `json:"keep_open"`
	Rules      []Rule `json:"rules"`
}
type Rule struct {
	Name            string   `json:"name"`
	Description     string   `json:"description,omitempty"`
	Directories     []string `json:"directories"`
	Patterns        []string `json:"patterns"`
	Hours           int      `json:"hours"`
	Recursive       *bool    `json:"recursive,omitempty"`
	DeleteParentDir bool     `json:"delete_parent_dir,omitempty"`
	Enabled         *bool    `json:"enabled,omitempty"`
}
type MaintenanceConfig struct {
	Enabled         bool `json:"enabled"`
	EveryHours      int  `json:"every_hours"`
	DryRun          bool `json:"dry_run"`
	Images          bool `json:"images"`
	BuildCache      bool `json:"build_cache"`
	BuildCacheHours int  `json:"build_cache_hours"`
	Journal         bool `json:"journal"`
	JournalDays     int  `json:"journal_days"`
	DiskWarnPercent int  `json:"disk_warn_percent"`
}

func DefaultConfig() Config {
	return Config{
		Backup: BackupConfig{Dir: "/var/lib/sourcenode/backups", Hour: 5, KeepDays: 14, KeepCount: 7, Include: []string{"all"}, Exclude: []string{"*.vpk", "*.dem", "steamapps", "Steam", "steamcmd", ".steam", "temps", "egg/cache"}, MinFreeBytes: 1 << 30, MaxBytes: 100 << 30, MaxFiles: 100000},
		Cleanup: CleanupConfig{EveryHours: 6, OnStart: true, KeepOpen: true, Rules: []Rule{
			{Name: "demos", Directories: []string{"."}, Patterns: []string{"*.dem"}, Hours: 168},
			{Name: "logs", Directories: []string{"."}, Patterns: []string{"L*.log", "errors_*.log"}, Hours: 336},
			{Name: "dumps", Directories: []string{"."}, Patterns: []string{"*.dmp", "*.mdmp"}, Hours: 168},
			{Name: "cores", Directories: []string{"."}, Patterns: []string{"core", "core.[0-9]*"}, Hours: 24},
		}}, Maintenance: MaintenanceConfig{EveryHours: 168, DryRun: true, Images: true, BuildCache: true, BuildCacheHours: 168, Journal: true, JournalDays: 14, DiskWarnPercent: 15},
	}
}

func safeRel(s string) bool {
	return s != "" && !strings.ContainsAny(s, "\\:\x00") && !path.IsAbs(s) && path.Clean(s) != ".." && !strings.HasPrefix(path.Clean(s), "../")
}
func (c Config) Validate() error {
	b := c.Backup
	if e := storagePath(b.Dir); e != nil {
		return e
	}
	if !filepath.IsAbs(b.Dir) || b.Hour < 0 || b.Hour > 23 || b.KeepDays < 0 || b.KeepCount < 0 || (b.KeepDays == 0 && b.KeepCount == 0) || b.MinFreeBytes < 0 || b.MaxBytes <= 0 || b.MaxFiles <= 0 || b.MaxFiles > 1000000 {
		return errors.New("invalid backup path, retention, schedule or bounds")
	}
	for _, p := range append(append([]string{}, b.Include...), b.Exclude...) {
		if !safeRel(p) {
			return fmt.Errorf("unsafe backup pattern %q", p)
		}
		if _, e := path.Match(p, ""); e != nil {
			return e
		}
	}
	if c.Cleanup.EveryHours < 0 || c.Cleanup.EveryHours > 8760 {
		return errors.New("invalid cleanup interval")
	}
	names := map[string]bool{}
	for _, r := range c.Cleanup.Rules {
		if r.Name == "" || names[r.Name] || len(r.Directories) == 0 || len(r.Patterns) == 0 || r.Hours < 0 || r.Hours > 876000 {
			return errors.New("invalid cleanup rule")
		}
		names[r.Name] = true
		for _, d := range r.Directories {
			if !safeRel(d) {
				return fmt.Errorf("unsafe cleanup directory %q", d)
			}
		}
		for _, p := range r.Patterns {
			if strings.ContainsAny(p, "/\\") {
				return errors.New("cleanup patterns must be basenames")
			}
			if _, e := path.Match(p, ""); e != nil {
				return e
			}
		}
	}
	m := c.Maintenance
	if m.EveryHours < 0 || m.EveryHours > 8760 || m.BuildCacheHours <= 0 || m.BuildCacheHours > 876000 || m.JournalDays <= 0 || m.JournalDays > 36500 || m.DiskWarnPercent < 0 || m.DiskWarnPercent > 100 {
		return errors.New("invalid maintenance schedule or retention")
	}
	return nil
}

// ID and Volume must be authoritative metadata, never broker-supplied paths.
// SharedRoot is a verified stock game tree, not another server's volume.
type Server struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Game       string `json:"game"`
	Volume     string `json:"volume"`
	SharedRoot string `json:"shared_root,omitempty"`
	Running    bool   `json:"running"`
	PID        int    `json:"pid,omitempty"`
}
type DiskInfo struct {
	Free  int64 `json:"free"`
	Total int64 `json:"total"`
}
type Notice struct {
	Event    string         `json:"event"`
	ServerID string         `json:"server_id,omitempty"`
	Fields   map[string]any `json:"fields,omitempty"`
}
type Options struct {
	Servers        func(context.Context) ([]Server, error)
	Lookup         func(context.Context, string) (Server, error)
	Command        func(context.Context, string, ...string) ([]byte, error)
	Disk           func(string) (DiskInfo, error)
	OpenFiles      func(context.Context, Server) (map[string]bool, error)
	RebootRequired func() (bool, error)
	WriteHostFile  func(context.Context, string, []byte, os.FileMode) error
	Notify         func(Notice)
	Now            func() time.Time
}

type BackupRecord struct {
	ServerID string    `json:"server_id"`
	Game     string    `json:"game"`
	Time     time.Time `json:"time"`
	Path     string    `json:"path"`
	Files    int       `json:"files"`
	Bytes    int64     `json:"bytes"`
	Raw      int64     `json:"raw"`
	Changed  []string  `json:"changed,omitempty"`
	Seconds  float64   `json:"seconds"`
}
type RuleStat struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}
type CleanupResult struct {
	ServerID string              `json:"server_id"`
	Time     time.Time           `json:"time"`
	Files    int                 `json:"files"`
	Bytes    int64               `json:"bytes"`
	Skipped  int                 `json:"skipped"`
	PerRule  map[string]RuleStat `json:"per_rule"`
	Errors   []string            `json:"errors,omitempty"`
}
type TaskResult struct {
	Task  string `json:"task"`
	Dry   bool   `json:"dry"`
	Items int    `json:"items"`
	Bytes int64  `json:"bytes"`
	Note  string `json:"note,omitempty"`
	Error string `json:"error,omitempty"`
}
type TaskTotal struct {
	Runs  int64 `json:"runs"`
	Items int64 `json:"items"`
	Bytes int64 `json:"bytes"`
}
type Status struct {
	Cleanup           map[string]CleanupResult `json:"cleanup"`
	CleanupTotals     map[string]CleanupResult `json:"cleanup_totals"`
	CleanupTotal      RuleStat                 `json:"cleanup_total"`
	Maintenance       []TaskResult             `json:"maintenance"`
	MaintenanceTotals map[string]TaskTotal     `json:"maintenance_totals"`
	MaintenanceRuns   int64                    `json:"maintenance_runs"`
	MaintenanceBytes  int64                    `json:"maintenance_bytes"`
	LastBackup        time.Time                `json:"last_backup"`
	LastCleanup       time.Time                `json:"last_cleanup"`
	LastMaintenance   time.Time                `json:"last_maintenance"`
	LastWatch         time.Time                `json:"last_watch"`
	LowDiskNotice     time.Time                `json:"low_disk_notice"`
	RebootNotified    bool                     `json:"reboot_notified"`
}
