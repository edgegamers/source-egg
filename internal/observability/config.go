// Package observability owns optional bounded node notices, console archives and
// crash evidence. Server identities and volume paths must come from trusted node
// discovery, never from a broker payload. Callers must redact their own secrets;
// the package additionally removes common credential assignments and controls.
package observability

import (
	"errors"
	"net/url"
	"path/filepath"
	"slices"
	"time"
)

type Config struct {
	Alerts  AlertsConfig `json:"alerts"`
	Logs    LogConfig    `json:"logs"`
	Crashes CrashConfig  `json:"crashes"`
}
type AlertsConfig struct {
	Enabled   bool     `json:"enabled"`
	Webhook   string   `json:"webhook,omitempty"`
	Events    []string `json:"events,omitempty"`
	Name      string   `json:"name,omitempty"`
	Username  string   `json:"username,omitempty"`
	AvatarURL string   `json:"avatar_url,omitempty"`
	QueueSize int      `json:"queue_size"`
}
type LogConfig struct {
	Enabled       bool   `json:"enabled"`
	Dir           string `json:"dir"`
	KeepDays      int    `json:"keep_days"`
	QueueSize     int    `json:"queue_size"`
	MaxDailyBytes int64  `json:"max_daily_bytes"`
}
type CrashConfig struct {
	Enabled        bool   `json:"enabled"`
	Dir            string `json:"dir"`
	KeepCount      int    `json:"keep_count"`
	KeepDays       int    `json:"keep_days"`
	QueueSize      int    `json:"queue_size"`
	MaxBundleBytes int64  `json:"max_bundle_bytes"`
}

var eventKinds = []string{"crash", "game.update", "game.update.failed", "push.failed", "restart.deferred", "node.update", "backup.done", "backup.failed", "cleanup.done", "maintenance.done", "disk.low", "reboot.required", "server.start", "server.stop", "server.restart", "framework.update"}

// EventKinds is an informational copy; mutating it cannot alter the allowlist.
var EventKinds = slices.Clone(eventKinds)

func DefaultConfig() Config {
	return Config{
		Alerts:  AlertsConfig{Events: []string{"all"}, Username: "sourcenode", QueueSize: 256},
		Logs:    LogConfig{KeepDays: 30, QueueSize: 2048, MaxDailyBytes: 256 << 20},
		Crashes: CrashConfig{KeepCount: 10, KeepDays: 30, QueueSize: 8, MaxBundleBytes: 1 << 30},
	}
}
func Defaults() Config { return DefaultConfig() }
func secureURL(s string) bool {
	u, e := url.Parse(s)
	return e == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}
func (c Config) Validate() error {
	if c.Alerts.QueueSize < 1 || c.Alerts.QueueSize > 65536 || c.Logs.QueueSize < 1 || c.Logs.QueueSize > 65536 || c.Crashes.QueueSize < 1 || c.Crashes.QueueSize > 64 {
		return errors.New("observability queue size outside bounds")
	}
	if c.Alerts.Enabled && !secureURL(c.Alerts.Webhook) {
		return errors.New("alerts webhook must be an HTTPS URL without credentials")
	}
	if c.Alerts.AvatarURL != "" && !secureURL(c.Alerts.AvatarURL) {
		return errors.New("alerts avatar must be an HTTPS URL without credentials")
	}
	for _, e := range c.Alerts.Events {
		if e != "all" && !slices.Contains(eventKinds, e) {
			return errors.New("unknown alert event")
		}
	}
	if (c.Logs.Enabled || c.Logs.Dir != "") && !filepath.IsAbs(c.Logs.Dir) || (c.Crashes.Enabled || c.Crashes.Dir != "") && !filepath.IsAbs(c.Crashes.Dir) {
		return errors.New("observability directories must be absolute when enabled")
	}
	if c.Logs.Dir != "" && protectedStorage(c.Logs.Dir) || c.Crashes.Dir != "" && protectedStorage(c.Crashes.Dir) {
		return errors.New("observability storage cannot be a protected filesystem directory")
	}
	if c.Logs.Dir != "" && c.Crashes.Dir != "" && filepath.Clean(c.Logs.Dir) == filepath.Clean(c.Crashes.Dir) {
		return errors.New("log and crash directories must differ")
	}
	if c.Logs.KeepDays < 0 || c.Crashes.KeepDays < 0 || c.Crashes.KeepCount < 0 {
		return errors.New("retention cannot be negative")
	}
	if c.Logs.MaxDailyBytes < 4096 || c.Logs.MaxDailyBytes > 10<<30 || c.Crashes.MaxBundleBytes < 4<<20 || c.Crashes.MaxBundleBytes > 4<<30 {
		return errors.New("archive byte limit outside bounds")
	}
	return nil
}

func protectedStorage(path string) bool {
	path = filepath.Clean(path)
	if path == filepath.VolumeName(path)+string(filepath.Separator) {
		return true
	}
	switch filepath.ToSlash(path) {
	case "/bin", "/boot", "/dev", "/etc", "/home", "/lib", "/lib64", "/media", "/mnt", "/opt", "/proc", "/root", "/run", "/sbin", "/srv", "/sys", "/tmp", "/usr", "/usr/local", "/var", "/var/cache", "/var/lib", "/var/log", "/var/tmp":
		return true
	}
	return false
}

const MaxLineLen = 4000
const MaxCrashLines = 500
const HardLogDays = 365
const MaxCrashFileBytes int64 = 512 << 20
const aggregateDelay = 3 * time.Second
