package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/edgegamers/source-egg/internal/addons"
	"github.com/edgegamers/source-egg/internal/fsx"
	"github.com/edgegamers/source-egg/internal/lifecycle"
	"github.com/edgegamers/source-egg/internal/observability"
	"github.com/edgegamers/source-egg/internal/operations"
	"github.com/edgegamers/source-egg/internal/passwords"
)

func (c Config) effectiveObservability() observability.Config {
	o := c.Observability
	if o.Alerts.Webhook == "" {
		o.Alerts.Webhook = c.Webhook
	}
	if o.Alerts.Webhook == "" {
		o.Alerts.Enabled = false
	}
	if o.Logs.Dir == "" {
		o.Logs.Dir = filepath.Join(c.StateDir, "logs")
	}
	if o.Crashes.Dir == "" {
		o.Crashes.Dir = filepath.Join(c.StateDir, "crashes")
	}
	return o
}
func (c Config) effectiveOperations() operations.Config {
	o := c.Operations
	if o.Backup.Dir == "" || o.Backup.Dir == "/var/lib/sourcenode/backups" {
		o.Backup.Dir = filepath.Join(c.StateDir, "backups")
	}
	return o
}

func (c Config) PrivateDirectories() []string {
	o := c.effectiveObservability()
	b := c.effectiveOperations()
	return []string{c.StateDir, c.passwordDirectory(), o.Logs.Dir, o.Crashes.Dir, b.Backup.Dir}
}
func (c Config) passwordOptions() passwords.Options {
	interval := time.Duration(c.PasswordIntervalHours) * time.Hour
	if c.PasswordMonths > 0 {
		interval = 0
	}
	months := c.PasswordMonths
	if c.PasswordCron != "" {
		interval = 0
		months = 0
	}
	return passwords.Options{Format: c.PasswordFormat, Words: c.PasswordWords, CalendarMonths: months, Interval: interval, Cron: c.PasswordCron, Timezone: c.PasswordTimezone, Wordlist: c.PasswordWordlist}
}
func (d *Daemon) initModules() error {
	var e error
	d.Observation, e = observability.New(d.Config.effectiveObservability(), observability.Options{Redact: d.redact})
	if e != nil {
		return e
	}
	d.Operations, e = operations.New(d.Config.effectiveOperations(), filepath.Join(d.Config.StateDir, "operations"), operations.Options{
		Servers: func(ctx context.Context) ([]operations.Server, error) {
			servers, e := d.Docker.Servers(ctx, d.Config.Images)
			if e != nil {
				return nil, e
			}
			out := []operations.Server{}
			for _, s := range servers {
				out = append(out, d.operationServer(s))
			}
			return out, nil
		},
		Lookup: func(ctx context.Context, id string) (operations.Server, error) {
			s, e := d.selectServer(id)
			if e != nil {
				return operations.Server{}, e
			}
			fresh, e := d.Docker.Inspect(ctx, s.ID, d.Config.Images)
			if e != nil {
				return operations.Server{}, e
			}
			if fresh.UUID != s.UUID || fresh.Identity != s.Identity || fresh.Volume != s.Volume {
				return operations.Server{}, errors.New("server identity changed")
			}
			return d.operationServer(fresh), nil
		},
		Command: runHostCommand,
		WriteHostFile: func(ctx context.Context, path string, data []byte, mode os.FileMode) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if path != operations.UpgradesFile {
				return errors.New("unapproved host configuration path")
			}
			root, e := os.OpenRoot(filepath.Dir(path))
			if e != nil {
				return e
			}
			defer root.Close()
			return fsx.Atomic(root, filepath.Base(path), data, mode)
		},
		Notify: func(n operations.Notice) {
			s, _ := d.selectServer(n.ServerID)
			d.notice(context.Background(), n.Event, s, n.Fields)
		},
	})
	return e
}

type cappedOutput struct{ bytes.Buffer }

func (w *cappedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := (1 << 20) - w.Len(); remaining > 0 {
		w.Buffer.Write(p[:min(len(p), remaining)])
	}
	return n, nil
}
func runHostCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var output cappedOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	e := cmd.Run()
	return output.Bytes(), e
}
func (d *Daemon) operationServer(s Server) operations.Server {
	out := operations.Server{ID: s.UUID, Name: s.Env["SERVER_HOSTNAME"], Game: s.Identity.Game, Volume: s.Volume, Running: s.Running, PID: s.PID}
	d.mu.Lock()
	ref := d.refs[s.UUID].Current
	d.mu.Unlock()
	if ref != "" {
		if g, e := d.Store.Generation(s.Identity, ref); e == nil {
			out.SharedRoot = g.Path
		}
	}
	return out
}
func (d *Daemon) observationServer(s Server) observability.Server {
	d.mu.Lock()
	state := d.states[s.UUID]
	d.mu.Unlock()
	out := observability.Server{UUID: s.UUID, Volume: s.Volume, Game: s.Identity.Game, Name: s.Env["SERVER_HOSTNAME"], Map: state.Map, Players: state.Players, Versions: map[string]string{}}
	if state.Name != "" {
		out.Name = state.Name
	}
	root, e := os.OpenRoot(s.Volume)
	if e == nil {
		defer root.Close()
		for _, name := range []string{"metamod", "sourcemod"} {
			var r addons.Receipt
			if fsx.ReadJSON(root, ".source/framework-"+name+".json", &r) == nil {
				out.Versions[name] = r.Version
			}
		}
	}
	return out
}
func (d *Daemon) redact(line string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, secret := range []string{d.Config.SteamPassword, d.Config.Webhook, d.Config.PasswordWebhook, d.Config.Observability.Alerts.Webhook} {
		if secret != "" {
			line = strings.ReplaceAll(line, secret, "[REDACTED]")
		}
	}
	for _, s := range d.servers {
		for _, name := range []string{"SERVER_PASSWORD", "RCON_PASSWORD", "STEAM_GSLT", "STEAM_PASSWORD"} {
			if v := s.Env[name]; v != "" {
				line = strings.ReplaceAll(line, v, "[REDACTED]")
			}
		}
	}
	for _, v := range d.generatedSecrets {
		if v != "" {
			line = strings.ReplaceAll(line, v, "[REDACTED]")
		}
	}
	return line
}

func redactServer(s Server, line string) string {
	for _, name := range []string{"SERVER_PASSWORD", "RCON_PASSWORD", "STEAM_GSLT", "STEAM_PASSWORD"} {
		if value := s.Env[name]; value != "" {
			line = strings.ReplaceAll(line, value, "[REDACTED]")
		}
	}
	return line
}
func (d *Daemon) observeFrameworks(ctx context.Context, s Server) {
	metadata := d.observationServer(s)
	d.mu.Lock()
	old := d.frameworkSeen[s.UUID]
	if d.frameworkSeen == nil {
		d.frameworkSeen = map[string]map[string]string{}
	}
	d.frameworkSeen[s.UUID] = metadata.Versions
	d.mu.Unlock()
	for name, version := range metadata.Versions {
		if old[name] != version {
			d.notice(ctx, "framework.update", s, map[string]any{"framework": name, "from": old[name], "to": version})
		}
	}
}
func (d *Daemon) runModules(ctx context.Context) {
	var wg sync.WaitGroup
	launch := func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() }
	launch(func() {
		if e := d.Observation.Run(ctx); e != nil {
			log.Printf("observability: %v", e)
		}
	})
	launch(func() {
		if e := d.Operations.Run(ctx); e != nil {
			log.Printf("operations: %v", e)
		}
	})
	launch(func() { d.runMetrics(ctx) })
	if d.Config.SelfUpdate.Enabled {
		launch(func() {
			exe, e := os.Executable()
			if e != nil {
				return
			}
			c := d.Config.SelfUpdate
			if c.Channel == "" {
				c.Channel = d.Config.NodeChannel
			}
			m, e := lifecycle.New(c, lifecycle.Options{BinaryPath: exe, BeforeRestart: d.beforeNodeRestart})
			if e != nil {
				log.Printf("self-update: %v", e)
				return
			}
			m.Run(ctx, d.Version, func(result lifecycle.Result) {
				errorText := ""
				if result.Err != nil {
					errorText = result.Err.Error()
				}
				d.mu.Lock()
				d.lastNodeUpdate = map[string]any{"version": result.Version, "updated": result.Updated, "checked_at": result.CheckedAt, "error": errorText}
				d.mu.Unlock()
				if result.Err != nil {
					log.Printf("self-update failed: %v", result.Err)
				}
			})
		})
	}
	launch(func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			if e := d.Observation.Sweep(ctx); e != nil {
				log.Printf("archive sweep: %v", e)
			}
			if e := d.pruneFrameworkCache(); e != nil {
				log.Printf("framework cache retention: %v", e)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
	d.reportNodeUpdate(ctx)
	wg.Wait()
	d.Observation.Close()
}

func (d *Daemon) pruneFrameworkCache() error {
	if !d.Config.FrameworkCache || d.Config.FrameworkKeepDays == 0 {
		return nil
	}
	protected := map[string]bool{}
	d.mu.Lock()
	servers := make([]Server, 0, len(d.servers))
	for _, s := range d.servers {
		servers = append(servers, s)
	}
	for name, pin := range d.frameworkPolicy.Fleet {
		protected[name+":"+pin] = true
	}
	for _, pins := range d.frameworkPolicy.Servers {
		for name, pin := range pins {
			protected[name+":"+pin] = true
		}
	}
	d.mu.Unlock()
	for _, s := range servers {
		root, e := os.OpenRoot(s.Volume)
		for _, name := range []string{"metamod", "sourcemod"} {
			if pin := s.Env[strings.ToUpper(name)+"_VERSION"]; pin != "" {
				protected[name+":"+pin] = true
			}
			if e == nil {
				var r addons.Receipt
				if fsx.ReadJSON(root, ".source/framework-"+name+".json", &r) == nil {
					protected[name+":"+r.Version] = true
				}
			}
		}
		if e == nil {
			root.Close()
		}
	}
	_, e := d.Cache.Prune(time.Duration(d.Config.FrameworkKeepDays)*24*time.Hour, protected)
	return e
}
func (d *Daemon) beforeNodeRestart(ctx context.Context, version string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	root, e := os.OpenRoot(d.Config.StateDir)
	if e != nil {
		return e
	}
	defer root.Close()
	return fsx.JSON(root, "pending-node-update.json", map[string]string{"version": version}, 0600)
}
func (d *Daemon) reportNodeUpdate(ctx context.Context) {
	root, e := os.OpenRoot(d.Config.StateDir)
	if e != nil {
		return
	}
	defer root.Close()
	var pending map[string]string
	if fsx.ReadJSON(root, "pending-node-update.json", &pending) == nil && pending["version"] == d.Version {
		d.notice(ctx, "node.update", Server{}, map[string]any{"version": d.Version})
		root.Remove("pending-node-update.json")
	}
}
func stringifyFields(fields map[string]any) map[string]string {
	out := map[string]string{}
	for k, v := range fields {
		out[k] = fmt.Sprint(v)
	}
	return out
}

func (d *Daemon) stopReason(s Server) string {
	d.mu.Lock()
	pending := d.pending[s.UUID]
	d.mu.Unlock()
	if !pending.RestartedAt.IsZero() && time.Since(pending.RestartedAt) < 5*time.Minute {
		return "game update restart"
	}
	if s.OOMKilled {
		return "out of memory"
	}
	if s.ExitCode == 0 {
		return "stopped cleanly"
	}
	if s.ExitCode >= 128 {
		return fmt.Sprintf("signal %d", s.ExitCode-128)
	}
	return "unexpected exit"
}
