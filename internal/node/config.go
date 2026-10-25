package node

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/edgegamers/source-egg/internal/passwords"
)

type RestartPolicy struct {
	Mode            string `json:"mode"`
	Window          string `json:"window"`
	MaxDelayMinutes int    `json:"max_delay_minutes"`
	WarnSeconds     int    `json:"warn_seconds"`
}
type Config struct {
	StateDir              string        `json:"state_dir"`
	SharedDir             string        `json:"shared_dir"`
	CacheDir              string        `json:"cache_dir"`
	SteamCMD              string        `json:"steamcmd"`
	Images                []string      `json:"images"`
	Shared                bool          `json:"shared"`
	FrameworkCache        bool          `json:"framework_cache"`
	PollSeconds           int           `json:"poll_seconds"`
	CheckMinutes          int           `json:"check_minutes"`
	Restart               RestartPolicy `json:"restart"`
	PasswordScope         string        `json:"password_scope"`
	PasswordIntervalHours int           `json:"password_interval_hours"`
	Webhook               string        `json:"webhook,omitempty"`
	PasswordWebhook       string        `json:"password_webhook,omitempty"`
	WingsConfig           string        `json:"wings_config"`
	SteamUsername         string        `json:"steam_username,omitempty"`
	SteamPassword         string        `json:"steam_password,omitempty"`
}

func DefaultConfig() Config {
	return Config{StateDir: "/var/lib/sourcenode", SharedDir: "/srv/source-shared", CacheDir: "/var/cache/sourcenode", SteamCMD: "/opt/steamcmd/steamcmd.sh", Images: []string{"ghcr.io/edgegamers/source-egg"}, Shared: true, FrameworkCache: true, PollSeconds: 5, CheckMinutes: 30, Restart: RestartPolicy{Mode: "empty", MaxDelayMinutes: 60, WarnSeconds: 60}, PasswordScope: "game", PasswordIntervalHours: 168}
}
func (c Config) Validate() error {
	for _, path := range []string{c.StateDir, c.SharedDir, c.CacheDir, c.SteamCMD} {
		if !filepath.IsAbs(path) || path == "/" {
			return errors.New("node paths must be absolute and non-root")
		}
	}
	if len(c.Images) == 0 || c.PollSeconds < 1 || c.CheckMinutes < 1 || c.PasswordIntervalHours < 0 || c.Restart.MaxDelayMinutes < 0 || c.Restart.WarnSeconds < 0 {
		return errors.New("invalid node settings")
	}
	if _, e := passwords.Key(c.PasswordScope, "validation", "css"); e != nil {
		return e
	}
	if c.Restart.Mode != "immediate" && c.Restart.Mode != "empty" && c.Restart.Mode != "window" {
		return errors.New("invalid restart policy")
	}
	if c.Restart.Mode == "window" {
		if _, _, e := parseWindow(c.Restart.Window); e != nil {
			return e
		}
	}
	for _, hook := range []string{c.Webhook, c.PasswordWebhook} {
		if hook != "" && !strings.HasPrefix(hook, "https://discord.com/api/webhooks/") {
			return errors.New("webhooks must use Discord HTTPS webhook URLs")
		}
	}
	return nil
}
func LoadConfig(path string) (Config, error) {
	c := DefaultConfig()
	f, e := os.Open(path)
	if e != nil {
		return c, e
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&c); e != nil {
		return c, e
	}
	return c, c.Validate()
}
func parseWindow(s string) (int, int, error) {
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		return 0, 0, errors.New("restart window must be HH:MM-HH:MM")
	}
	a, e := time.Parse("15:04", parts[0])
	if e != nil {
		return 0, 0, e
	}
	b, e := time.Parse("15:04", parts[1])
	if e != nil {
		return 0, 0, e
	}
	return a.Hour()*60 + a.Minute(), b.Hour()*60 + b.Minute(), nil
}
func Due(p RestartPolicy, since, now time.Time, players int) bool {
	if p.Mode == "immediate" || (p.Mode == "empty" && players == 0) {
		return true
	}
	if p.MaxDelayMinutes > 0 && now.Sub(since) >= time.Duration(p.MaxDelayMinutes)*time.Minute {
		return true
	}
	if p.Mode == "window" {
		a, b, e := parseWindow(p.Window)
		if e != nil {
			return false
		}
		m := now.Hour()*60 + now.Minute()
		if a <= b {
			return m >= a && m < b
		}
		return m >= a || m < b
	}
	return false
}
