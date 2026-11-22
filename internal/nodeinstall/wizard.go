package nodeinstall

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/edgegamers/source-egg/internal/fsx"
	"github.com/edgegamers/source-egg/internal/game"
	"github.com/edgegamers/source-egg/internal/node"
	"github.com/edgegamers/source-egg/internal/passwords"
	"github.com/edgegamers/source-egg/internal/terminalui"
)

type summaryValue struct{ section, label, value string }
type wizard struct {
	input   *bufio.Reader
	output  io.Writer
	ui      terminalui.UI
	err     error
	section string
	summary []summaryValue
}

func (w *wizard) sectionTitle(title, help string) {
	if w.err != nil {
		return
	}
	w.section = title
	w.ui.Rule(title)
	if help != "" {
		w.ui.Help(help)
	}
}
func (w *wizard) ask(label, def string, valid func(string) error, help ...string) string {
	if w.err != nil {
		return def
	}
	for {
		fmt.Fprintf(w.output, "\n  %s\n", w.ui.Bold(label))
		for _, h := range help {
			w.ui.Help(h)
		}
		fmt.Fprintf(w.output, "  %s [%s]: ", w.ui.Style("36", ">"), w.ui.Style("36", def))
		line, err := w.input.ReadString('\n')
		if err != nil && len(line) == 0 {
			w.err = fmt.Errorf("setup interrupted before saving: %w", err)
			return def
		}
		value := strings.TrimSpace(line)
		if value == "" {
			value = def
		}
		if valid != nil {
			if e := valid(value); e != nil {
				w.ui.Help(e.Error())
				continue
			}
		}
		return value
	}
}
func (w *wizard) record(label, value string) {
	w.summary = append(w.summary, summaryValue{w.section, label, value})
}
func (w *wizard) text(dst *string, label string, valid func(string) error, help ...string) {
	*dst = w.ask(label, *dst, valid, help...)
	w.record(label, *dst)
}
func (w *wizard) optional(dst *string, label string, valid func(string) error, help ...string) {
	help = append(help, "Enter keeps the current value; - clears it.")
	v := w.ask(label, *dst, func(s string) error {
		if s == "-" {
			s = ""
		}
		if valid != nil {
			return valid(s)
		}
		return nil
	}, help...)
	if v == "-" {
		v = ""
	}
	*dst = v
	w.record(label, v)
}
func (w *wizard) yes(dst *bool, label string, help ...string) {
	def := "n"
	if *dst {
		def = "y"
	}
	help = append([]string{"y / n"}, help...)
	v := w.ask(label, def, func(s string) error {
		switch strings.ToLower(s) {
		case "y", "yes", "true", "n", "no", "false":
			return nil
		}
		return errors.New("Please answer y or n.")
	}, help...)
	*dst = strings.EqualFold(v, "y") || strings.EqualFold(v, "yes") || strings.EqualFold(v, "true")
	w.record(label, strconv.FormatBool(*dst))
}
func (w *wizard) number(dst *int, label string, min, max int, help ...string) {
	v := w.ask(label, strconv.Itoa(*dst), func(s string) error {
		n, e := strconv.Atoi(s)
		if e != nil || n < min || n > max {
			return fmt.Errorf("Enter a whole number from %d to %d.", min, max)
		}
		return nil
	}, help...)
	if w.err == nil {
		*dst, _ = strconv.Atoi(v)
	}
	w.record(label, strconv.Itoa(*dst))
}
func (w *wizard) choice(dst *string, label string, choices []string, help ...string) {
	for _, v := range choices {
		help = append(help, v)
	}
	w.text(dst, label, func(s string) error {
		for _, c := range choices {
			if strings.SplitN(c, " — ", 2)[0] == s {
				return nil
			}
		}
		return errors.New("Choose one of the listed values.")
	}, help...)
}
func (w *wizard) list(dst *[]string, label string, valid func([]string) error, help ...string) {
	def := strings.Join(*dst, ", ")
	if def == "" {
		def = "none"
	}
	split := func(s string) []string {
		if s == "none" || s == "-" {
			return []string{}
		}
		var out []string
		for _, p := range strings.Split(s, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	help = append(help, "Comma-separated values; none clears the list.")
	v := w.ask(label, def, func(s string) error {
		if valid != nil {
			return valid(split(s))
		}
		return nil
	}, help...)
	*dst = split(v)
	w.record(label, strings.Join(*dst, ", "))
}
func absolute(s string) error {
	if !filepath.IsAbs(s) || filepath.Clean(s) == "/" {
		return errors.New("Use an absolute path to a dedicated directory or file.")
	}
	return nil
}
func (w *wizard) path(dst *string, label string, help ...string) {
	w.text(dst, label, absolute, help...)
}
func (w *wizard) hook(dst *string, label string) {
	def := "disabled"
	if *dst != "" {
		def = "configured"
	}
	v := w.ask(label, def, func(s string) error {
		if s == def || s == "-" {
			return nil
		}
		if strings.HasPrefix(s, "https://discord.com/api/webhooks/") {
			return nil
		}
		return errors.New("Use a Discord HTTPS webhook URL, or - to disable.")
	}, "Enter keeps the saved value; - clears it. Saved credentials are never displayed.")
	if v == "-" {
		*dst = ""
	} else if v != def {
		*dst = v
	}
	state := "disabled"
	if *dst != "" {
		state = "configured (hidden)"
	}
	w.record(label, state)
}
func games(s string) ([]string, error) {
	if s == "none" || s == "-" || s == "" {
		return nil, nil
	}
	if s == "all" {
		return []string{"csgo", "css", "dods", "l4d2", "tf2"}, nil
	}
	var result []string
	seen := map[string]bool{}
	for _, key := range strings.Split(s, ",") {
		p, e := game.Lookup(strings.TrimSpace(key))
		if e != nil {
			return nil, errors.New("Use csgo, css, dods, l4d2, tf2, all or none.")
		}
		if !seen[p.Key] {
			result = append(result, p.Key)
			seen[p.Key] = true
		}
	}
	return result, nil
}

// Configure collects a complete reviewable configuration, without writing files.
func Configure(c node.Config, input io.Reader, output io.Writer) (node.Config, error) {
	c, _, err := configure(c, input, output)
	return c, err
}
func configure(c node.Config, input io.Reader, output io.Writer) (node.Config, *node.FrameworkPolicy, error) {
	w := &wizard{input: bufio.NewReader(input), output: output, ui: terminalui.New(output)}
	w.ui.Banner()
	fmt.Fprintf(output, "\n  %s\n", w.ui.Bold("Source Egg node — Installer"))
	w.ui.Help("Install on the Wings node. No game servers need to exist yet. Enter keeps each current value.")
	w.ui.Table([]string{"Module", "Purpose"}, [][]string{{"Game files", "Shared VPKs and game updates"}, {"Frameworks", "Metamod / SourceMod cache and version locks"}, {"Passwords", "Scheduled opt-in join passwords"}, {"Operations", "Backups, cleanup and host maintenance"}, {"Observability", "Discord, console archives, crash bundles and metrics"}})
	w.ui.Help("This installer will review your settings, place the binary, write private configuration, and install and start the systemd service.")
	proceed := true
	w.yes(&proceed, "Proceed?")
	if !proceed && w.err == nil {
		w.err = errors.New("setup cancelled before saving")
	}
	w.sectionTitle("Node", "Release and automatic node updates.")
	w.choice(&c.NodeChannel, "Update channel", []string{"stable — stable releases", "beta — include prereleases", "dev — manual binary updates only"})
	if c.NodeChannel != "dev" {
		w.yes(&c.SelfUpdate.Enabled, "Self-update automatically")
		if c.SelfUpdate.Enabled {
			w.number(&c.SelfUpdate.CheckMinutes, "Minutes between node update checks", 1, 525600)
		}
	}
	w.sectionTitle("Game files", "Shared base-game files; private maps, configs and plugins remain in each server volume.")
	w.yes(&c.Shared, "Share base-game VPK files")
	if c.Shared {
		w.path(&c.SharedDir, "Shared game directory")
		w.path(&c.SteamCMD, "SteamCMD executable")
		list := strings.Join(c.PrefetchGames, ",")
		if list == "" {
			list = "none"
		}
		list = w.ask("Games to prepare now", list, func(s string) error { _, e := games(s); return e }, "csgo, css, dods, l4d2, tf2, all or none. None downloads on first server start.")
		c.PrefetchGames, _ = games(list)
		w.record("Predownload games", list)
		w.choice(&c.PushMethod, "How VPK files reach servers", []string{"symlink — read-only bind mount and links, minimal disk usage", "hardlink — shared inodes; same filesystem required", "copy — private full copies; more disk space"})
		w.number(&c.MaxWorkers, "Parallel server preparations", 1, 256, "Higher values prepare more servers at once and increase disk load.")
		w.yes(&c.SteamValidate, "Run node SteamCMD with validate")
		w.number(&c.CheckMinutes, "Game update check interval minutes", 1, 1440)
		w.yes(&c.AutoRestart, "Restart servers through Wings after a game update")
		if c.AutoRestart {
			w.choice(&c.Restart.Mode, "Game-update restart policy", []string{"immediate — restart after the update", "empty — wait until nobody is connected or the maximum delay expires", "window — restart when empty, inside the window, or at the maximum delay"})
			if c.Restart.Mode == "window" {
				if c.Restart.Window == "" {
					c.Restart.Window = "04:00-06:00"
				}
				w.text(&c.Restart.Window, "Restart window in node local time", func(s string) error { v := c; v.Restart.Window = s; return v.Validate() }, "HH:MM-HH:MM; may cross midnight.")
			}
			if c.Restart.Mode != "immediate" {
				w.number(&c.Restart.MaxDelayMinutes, "Maximum restart delay minutes", 0, 10080, "0 = never force.")
				w.number(&c.Restart.WarnSeconds, "Restart countdown seconds", 0, 3600, "0 = no in-game warning.")
			}
		}
	}
	w.sectionTitle("Framework cache", "Metamod and SourceMod downloads and node version locks.")
	w.yes(&c.FrameworkCache, "Cache Metamod/SourceMod downloads")
	var pins *node.FrameworkPolicy
	if c.FrameworkCache {
		w.path(&c.CacheDir, "Framework cache directory")
		w.number(&c.FrameworkRefreshMinutes, "Minutes a release answer stays fresh", 1, 10080)
		w.number(&c.FrameworkKeepDays, "Days to keep unused framework downloads", 0, 36500, "0 = keep forever. Installed and pinned releases are protected.")
		w.yes(&c.FrameworkPinsEnabled, "Honor node framework version locks", "Panel exact pins take precedence. Empty fleet versions follow latest.")
		if c.FrameworkPinsEnabled && w.err == nil {
			p, e := loadSetupPins(c.StateDir)
			if e != nil {
				w.err = e
			} else {
				for _, name := range []string{"metamod", "sourcemod"} {
					v := p.Fleet[name]
					w.optional(&v, "Fleet "+name+" version", validPin, "Exact upstream release tag; - follows latest.")
					if v == "" {
						delete(p.Fleet, name)
					} else {
						p.Fleet[name] = v
					}
				}
				pins = &p
			}
		}
	}
	configurePasswords(&c, w)
	configureOperations(&c, w)
	if w.err != nil {
		return c, nil, w.err
	}
	if err := c.Validate(); err != nil {
		return c, nil, err
	}
	w.ui.Rule("Summary")
	section := ""
	for _, r := range w.summary {
		if r.section != section {
			fmt.Fprintf(output, "\n  %s\n", w.ui.Bold(r.section))
			section = r.section
		}
		w.ui.KV(r.label, r.value)
	}
	if c.PasswordEnabled {
		opts := passwordOptions(c)
		if next, e := opts.Next(time.Now()); e == nil && !next.IsZero() {
			w.ui.KV("Next new-password rotation", next.Format(time.RFC3339))
			w.ui.Help("Existing passwords retain their saved due dates until their next rotation.")
		}
	}
	confirm := true
	w.yes(&confirm, "Install with these settings?")
	if !confirm && w.err == nil {
		w.err = errors.New("setup cancelled before saving")
	}
	return c, pins, w.err
}
func validPin(s string) error {
	if len(s) > 128 || strings.ContainsAny(s, "\n\r\x00/\\") {
		return errors.New("Use an upstream release tag without path separators.")
	}
	return nil
}
func loadSetupPins(dir string) (node.FrameworkPolicy, error) {
	p := node.FrameworkPolicy{Fleet: map[string]string{}, Servers: map[string]map[string]string{}}
	root, e := os.OpenRoot(dir)
	if os.IsNotExist(e) {
		return p, nil
	}
	if e != nil {
		return p, e
	}
	defer root.Close()
	e = fsx.ReadJSON(root, "framework-pins.json", &p)
	if os.IsNotExist(e) {
		return p, nil
	}
	if e != nil {
		return p, e
	}
	if p.Fleet == nil || p.Servers == nil {
		return p, errors.New("invalid framework pin state")
	}
	return p, nil
}
func passwordOptions(c node.Config) passwords.Options {
	o := passwords.Options{Format: c.PasswordFormat, Words: c.PasswordWords, CalendarMonths: c.PasswordMonths, Interval: time.Duration(c.PasswordIntervalHours) * time.Hour, Cron: c.PasswordCron, Timezone: c.PasswordTimezone, Wordlist: c.PasswordWordlist}
	if o.Cron != "" {
		o.Interval = 0
		o.CalendarMonths = 0
	} else if o.CalendarMonths > 0 {
		o.Interval = 0
	}
	return o
}
func configurePasswords(c *node.Config, w *wizard) {
	w.sectionTitle("Passwords", "Join passwords are opt-in per server. A rotation replaces the password and restarts participating running servers.")
	w.yes(&c.PasswordEnabled, "Enable node-managed passwords", "Disabling keeps each server's cached assignment and stops regeneration.")
	if !c.PasswordEnabled {
		return
	}
	w.choice(&c.PasswordScope, "Dev join-password sharing", []string{"game — one password per game on this node", "server — a unique password per server", "node — one shared password across participating servers"})
	w.choice(&c.PasswordFormat, "Join-password format", []string{"eff — random words from the EFF dictionary", "token — secure random token"})
	if c.PasswordFormat == "eff" {
		if c.PasswordWords == 0 {
			c.PasswordWords = 3
		}
		w.number(&c.PasswordWords, "Words per join-password phrase", 3, 16)
		w.optional(&c.PasswordWordlist, "Dictionary file", func(s string) error {
			if s == "" {
				return nil
			}
			return absolute(s)
		}, "Empty uses the bundled EFF list. Custom lists need at least 7772 distinct usable words.")
	}
	w.optional(&c.PasswordDir, "Private password state directory", func(s string) error {
		if s == "" {
			return nil
		}
		return absolute(s)
	}, "Empty follows state_dir/passwords. Changing this path requires moving existing state first.")
	mode := "hours"
	switch {
	case c.PasswordCron != "":
		mode = "cron"
	case c.PasswordMonths == 1:
		mode = "monthly"
	case c.PasswordMonths > 0:
		mode = "months"
	case c.PasswordIntervalHours == 0:
		mode = "never"
	case c.PasswordIntervalHours == 24:
		mode = "daily"
	case c.PasswordIntervalHours == 168:
		mode = "weekly"
	case c.PasswordIntervalHours == 336:
		mode = "bi-weekly"
	}
	w.choice(&mode, "Password rotation schedule", []string{"cron — run at specific calendar times", "daily — every 24 hours", "weekly — every 7 days", "bi-weekly — every 14 days", "monthly — every calendar month", "hours — custom elapsed hours", "months — custom calendar months", "never — retain the generated password"})
	hours, months := c.PasswordIntervalHours, c.PasswordMonths
	c.PasswordIntervalHours = 0
	c.PasswordMonths = 0
	if mode != "cron" {
		c.PasswordCron = ""
	}
	switch mode {
	case "cron":
		if c.PasswordTimezone == "" {
			c.PasswordTimezone = "UTC"
		}
		w.text(&c.PasswordTimezone, "Password schedule timezone", func(s string) error { _, e := time.LoadLocation(s); return e }, "IANA timezone, such as America/Chicago or UTC.")
		if c.PasswordCron == "" {
			c.PasswordCron = "0 4 * * 1"
		}
		w.text(&c.PasswordCron, "Password cron expression", func(s string) error { _, e := passwords.NextCron(time.Now(), s, c.PasswordTimezone); return e }, "minute hour day-of-month month weekday; 0 4 * * 1 = Mondays at 04:00. Lists, ranges and steps are supported.")
	case "daily":
		c.PasswordIntervalHours = 24
	case "weekly":
		c.PasswordIntervalHours = 168
	case "bi-weekly":
		c.PasswordIntervalHours = 336
	case "monthly":
		c.PasswordMonths = 1
	case "hours":
		c.PasswordIntervalHours = hours
		w.number(&c.PasswordIntervalHours, "Join-password rotation hours", 1, 876000)
	case "months":
		c.PasswordMonths = max(1, months)
		w.number(&c.PasswordMonths, "Calendar months per rotation", 1, 1200)
	}
	w.hook(&c.PasswordWebhook, "Private Discord password webhook")
}
