package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/edgegamers/source-egg/internal/addons"
	"github.com/edgegamers/source-egg/internal/content"
	"github.com/edgegamers/source-egg/internal/fsx"
	"github.com/edgegamers/source-egg/internal/game"
	"github.com/edgegamers/source-egg/internal/metrics"
	"github.com/edgegamers/source-egg/internal/observability"
	"github.com/edgegamers/source-egg/internal/operations"
	"github.com/edgegamers/source-egg/internal/passwords"
)

type State struct {
	Map        string    `json:"map"`
	Name       string    `json:"name"`
	Boot       string    `json:"boot"`
	Players    int       `json:"players"`
	UpdatedAt  time.Time `json:"updated_at"`
	Generation string    `json:"generation"`
	Ready      bool      `json:"ready"`
}
type Reference struct {
	Current string `json:"current"`
	Pending string `json:"pending"`
}
type pending struct {
	Generation  string
	Since       time.Time
	WarnedAt    time.Time
	RestartedAt time.Time
}
type Daemon struct {
	Config           Config
	frameworkPolicy  FrameworkPolicy
	Version          string
	Observation      *observability.Manager
	Operations       *operations.Manager
	generatedSecrets map[string]string
	frameworkSeen    map[string]map[string]string
	lastNodeUpdate   map[string]any
	startedAt        time.Time
	metricsMu        sync.RWMutex // Only snapshot publication and reads.
	metricsSampleMu  sync.Mutex   // Serializes sampling, prior counters and deltas.
	metricsPrevious  map[string]metrics.DockerStats
	metricSnapshot   metrics.Snapshot
	metricAt         time.Time
	hostCounter      metrics.HostCounter
	Docker           *Docker
	Store            *content.Store
	Cache            *addons.Cache
	Passwords        *passwords.Store
	mu               sync.Mutex
	states           map[string]State
	servers          map[string]Server
	refs             map[string]Reference
	pins             map[string]string
	pending          map[string]pending
	socketBoot       map[string]string
	passwordRestarts map[string]string
	listeners        map[string]net.Listener
	prepare          sync.RWMutex // Shared preparations; exclusive prune/pin changes.
	prepareWorkers   chan struct{}
	prepareJobs      map[preparationKey]*preparationCall
	updating         sync.Mutex
	notify           sync.Mutex
}

func New(cfg Config) (*Daemon, error) {
	if e := cfg.Validate(); e != nil {
		return nil, e
	}
	if e := os.MkdirAll(cfg.StateDir, 0700); e != nil {
		return nil, e
	}
	var pw *passwords.Store
	var e error
	if cfg.PasswordEnabled {
		pw, e = passwords.OpenWithOptions(cfg.passwordDirectory(), cfg.passwordOptions())
		if e != nil {
			return nil, e
		}
	}
	d := &Daemon{Config: cfg, Docker: NewDocker(), Store: &content.Store{Dir: cfg.SharedDir, Installer: &content.Steam{Executable: cfg.SteamCMD, Username: cfg.SteamUsername, Password: cfg.SteamPassword, Validate: cfg.SteamValidate}}, Cache: addons.NewCache(cfg.CacheDir, nil), Passwords: pw, states: map[string]State{}, servers: map[string]Server{}, refs: map[string]Reference{}, pending: map[string]pending{}, socketBoot: map[string]string{}, passwordRestarts: map[string]string{}, listeners: map[string]net.Listener{}}
	root, e := os.OpenRoot(cfg.StateDir)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	e = fsx.ReadJSON(root, "references.json", &d.refs)
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	if d.refs == nil {
		return nil, errors.New("invalid saved references")
	}
	e = fsx.ReadJSON(root, "pending.json", &d.pending)
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	if d.pending == nil {
		return nil, errors.New("invalid saved pending updates")
	}
	d.pins = map[string]string{}
	e = fsx.ReadJSON(root, "pins.json", &d.pins)
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	if d.pins == nil {
		return nil, errors.New("invalid saved pins")
	}
	if e = d.loadFrameworkPolicy(); e != nil {
		return nil, e
	}
	d.generatedSecrets = map[string]string{}
	d.frameworkSeen = map[string]map[string]string{}
	if e = d.initModules(); e != nil {
		return nil, e
	}
	d.Cache.Refresh = time.Duration(cfg.FrameworkRefreshMinutes) * time.Minute
	return d, nil
}
func (d *Daemon) saveRefs() error {
	root, e := os.OpenRoot(d.Config.StateDir)
	if e != nil {
		return e
	}
	defer root.Close()
	if e = fsx.JSON(root, "references.json", d.refs, 0600); e != nil {
		return e
	}
	return fsx.JSON(root, "pending.json", d.pending, 0600)
}
func (d *Daemon) Handler(bound Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		reply := func(code int, out Response) { w.WriteHeader(code); json.NewEncoder(w).Encode(out) }
		if r.Method != "POST" || r.URL.Path != "/" {
			reply(404, Response{Error: "unknown operation"})
			return
		}
		var req Request
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&req) != nil || req.Version != ProtocolVersion || req.Identity != bound.Identity {
			reply(400, Response{Error: "invalid protocol or game identity"})
			return
		}
		current, e := d.Docker.Inspect(r.Context(), bound.ID, d.Config.Images)
		if e != nil || !current.Running || current.UUID != bound.UUID || current.Boot != bound.Boot || current.Identity != bound.Identity {
			reply(409, Response{Error: "server identity or boot changed"})
			return
		}
		out, e := d.handle(r.Context(), current, req)
		if e != nil {
			log.Printf("node operation %s failed for %s: %v", req.Action, current.UUID, e)
			if req.Action == "prepare" || req.Action == "applied" {
				d.notice(r.Context(), "push.failed", current, map[string]any{"error": e.Error()})
			}
			reply(503, Response{Error: "operation unavailable; see node journal"})
			return
		}
		reply(200, out)
	})
}
func (d *Daemon) handle(ctx context.Context, s Server, r Request) (Response, error) {
	out := Response{}
	switch r.Action {
	case "prepare":
		return d.prepareServer(ctx, s, r.HostDelivery)
	case "applied":
		d.mu.Lock()
		defer d.mu.Unlock()
		ref := d.refs[s.UUID]
		if r.Generation == "" || ref.Pending != r.Generation {
			return out, errors.New("unexpected applied generation")
		}
		ref.Current = r.Generation
		ref.Pending = ""
		d.refs[s.UUID] = ref
		delete(d.pending, s.UUID)
		return out, d.saveRefs()
	case "state":
		if r.Players < -1 || r.Players > 128 || len(r.Map) > 128 || len(r.Name) > 128 {
			return out, errors.New("invalid server state")
		}
		d.mu.Lock()
		d.states[s.UUID] = State{Boot: s.Boot, Players: r.Players, UpdatedAt: time.Now(), Generation: r.Generation, Ready: r.Ready, Map: r.Map, Name: r.Name}
		p := d.pending[s.UUID]
		d.mu.Unlock()
		if r.Ready {
			d.observeFrameworks(ctx, s)
		}
		if d.Config.AutoRestart && !p.WarnedAt.IsZero() {
			out.RestartSeconds = int(time.Until(p.WarnedAt.Add(time.Duration(d.Config.Restart.WarnSeconds) * time.Second)).Seconds())
			if out.RestartSeconds < 1 {
				out.RestartSeconds = 1
			}
		}
	case "password":
		if !d.Config.PasswordEnabled {
			out.PasswordDisabled = true
			return out, nil
		}
		a, e := d.password(ctx, s)
		if e != nil {
			return out, e
		}
		out.Password = &a
	case "password-restart":
		a, e := d.password(ctx, s)
		if e != nil {
			return out, e
		}
		if a.Revision != r.Revision {
			return out, errors.New("stale password revision")
		}
		d.mu.Lock()
		already := d.passwordRestarts[s.UUID] == a.Revision
		d.mu.Unlock()
		if already {
			return out, nil
		}
		if e = d.restart(ctx, s); e != nil {
			return out, e
		}
		d.mu.Lock()
		d.passwordRestarts[s.UUID] = a.Revision
		d.mu.Unlock()
	case "logs":
		if len(r.Console) > 32 {
			return out, errors.New("console batch exceeds limit")
		}
		for _, line := range r.Console {
			if len(line) > 4000 {
				return out, errors.New("console line exceeds limit")
			}
		}
		if s.Env["ENABLE_LOG_FILES"] != "0" {
			metadata := d.observationServer(s)
			for _, line := range r.Console {
				d.Observation.Log(metadata, "console", redactServer(s, line))
			}
		}
	case "crash":
		if len(r.Console) > 500 {
			return out, errors.New("crash tail exceeds limit")
		}
		for _, line := range r.Console {
			if len(line) > 512 {
				return out, errors.New("crash line exceeds limit")
			}
		}
		lines := make([]string, len(r.Console))
		for i, line := range r.Console {
			lines[i] = redactServer(s, line)
		}
		out.Queued = d.Observation.Crash(d.observationServer(s), observability.CrashReport{ExitCode: r.ExitCode, Lines: lines})
		if out.Queued {
			d.notice(ctx, "crash", s, map[string]any{"exit_code": r.ExitCode, "bundle": "queued; use sourcenode crashes " + s.UUID})
		}
	case "boot-failed":
		if len(r.Message) > 512 {
			return out, errors.New("boot failure message exceeds limit")
		}
		d.notice(ctx, "push.failed", s, map[string]any{"error": redactServer(s, r.Message)})
	default:
		return out, errors.New("unsupported node operation")
	}
	return out, nil
}
func frameworkEnabled(env map[string]string, name string) bool {
	if name == "metamod" && env["INSTALL_SOURCEMOD"] != "0" {
		return true
	}
	return env["INSTALL_"+strings.ToUpper(name)] != "0"
}
func (d *Daemon) password(ctx context.Context, s Server) (passwords.Assignment, error) {
	if !d.Config.PasswordEnabled || d.Passwords == nil || s.Env["GENERATE_PASSWORD"] != "1" {
		return passwords.Assignment{}, errors.New("generated passwords not enabled")
	}
	scope := s.Env["PASSWORD_SCOPE"]
	if scope == "" || scope == "default" {
		scope = d.Config.PasswordScope
	}
	a, e := d.Passwords.Get(scope, s.UUID, s.Identity.Game, time.Now())
	if e != nil {
		return a, e
	}
	d.mu.Lock()
	d.generatedSecrets[s.UUID] = a.Password
	d.mu.Unlock()
	if d.Config.PasswordWebhook != "" {
		d.deliverPassword(ctx, s, scope, a)
	}
	return a, nil
}
func (d *Daemon) Run(ctx context.Context) error {
	d.startedAt = time.Now()
	lock, err := d.lock()
	if err != nil {
		return err
	}
	defer lock.Close()
	if e := d.control(); e != nil {
		return e
	}
	if e := os.MkdirAll(d.Config.SharedDir, 0755); e != nil {
		return e
	}
	if e := os.MkdirAll(d.Config.CacheDir, 0755); e != nil {
		return e
	}
	defer func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		for _, l := range d.listeners {
			l.Close()
		}
	}()
	if e := d.discover(ctx); e != nil {
		return e
	}
	go d.warmup(ctx)
	moduleCtx, cancelModules := context.WithCancel(ctx)
	moduleDone := make(chan struct{})
	go func() { d.runModules(moduleCtx); close(moduleDone) }()
	defer func() { cancelModules(); <-moduleDone }()
	ticker := time.NewTicker(time.Duration(d.Config.PollSeconds) * time.Second)
	defer ticker.Stop()
	updates := time.NewTicker(time.Duration(d.Config.CheckMinutes) * time.Minute)
	defer updates.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if e := d.discover(ctx); e != nil {
				log.Printf("discovery failed: %v", e)
			}
			d.rollout(ctx)
		case <-updates.C:
			go d.checkUpdates(ctx)
		}
	}
}
func (d *Daemon) discover(ctx context.Context) error {
	servers, e := d.Docker.Servers(ctx, d.Config.Images)
	if e != nil {
		return e
	}
	d.mu.Lock()
	previous := d.servers
	d.mu.Unlock()
	fresh := map[string]Server{}
	for _, s := range servers {
		fresh[s.UUID] = s
		if !s.Running {
			continue
		}
		d.mu.Lock()
		boot := d.socketBoot[s.UUID]
		d.mu.Unlock()
		if boot == s.Boot {
			continue
		}
		if e = d.attach(s); e != nil {
			log.Printf("node attach failed for %s: %v", s.UUID, e)
			d.notice(ctx, "push.failed", s, map[string]any{"error": e.Error()})
			continue
		}
	}
	d.mu.Lock()
	d.servers = fresh
	d.mu.Unlock()
	for id, s := range fresh {
		old, exists := previous[id]
		if s.Running && (!exists || !old.Running || old.Boot != s.Boot) {
			d.notice(ctx, "server.start", s, nil)
			go func() {
				if e := d.Operations.OnStart(ctx, d.operationServer(s)); e != nil {
					log.Printf("on-start cleanup: %v", e)
				}
			}()
		} else if exists && old.Running && !s.Running {
			d.notice(ctx, "server.stop", s, map[string]any{"reason": d.stopReason(s), "exit_code": s.ExitCode})
		}
	}
	for id, s := range previous {
		if _, exists := fresh[id]; !exists && s.Running {
			d.notice(ctx, "server.stop", s, map[string]any{"reason": "container removed"})
		}
	}
	d.reconcile()
	d.mu.Lock()
	root, e := os.OpenRoot(d.Config.StateDir)
	if e == nil {
		e = fsx.JSON(root, "status.json", d.snapshot(), 0600)
		root.Close()
	}
	d.mu.Unlock()
	return e
}
func (d *Daemon) attach(s Server) error {
	dir := filepath.Join(d.Config.StateDir, "sockets", s.UUID)
	if e := os.MkdirAll(dir, 0755); e != nil {
		return e
	}
	path := filepath.Join(dir, "node.sock")
	d.mu.Lock()
	if l := d.listeners[s.UUID]; l != nil {
		l.Close()
	}
	delete(d.socketBoot, s.UUID)
	d.mu.Unlock()
	os.Remove(path)
	listener, e := net.Listen("unix", path)
	if e != nil {
		return e
	}
	if e = os.Chmod(path, 0666); e != nil {
		listener.Close()
		return e
	}
	server := &http.Server{Handler: d.Handler(s), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	go server.Serve(listener)
	if e = BindReadOnly(s.PID, dir, SocketDir); e == nil && d.Config.Shared && d.Config.PushMethod == "symlink" && s.Env["SOURCE_MODE"] != "standalone" {
		e = BindReadOnly(s.PID, d.Config.SharedDir, SharedMount)
	}
	if e == nil && d.Config.FrameworkCache {
		e = BindReadOnly(s.PID, d.Config.CacheDir, CacheMount)
	}
	if e != nil {
		listener.Close()
		return e
	}
	d.mu.Lock()
	d.listeners[s.UUID] = listener
	d.socketBoot[s.UUID] = s.Boot
	d.mu.Unlock()
	return nil
}
func (d *Daemon) restart(ctx context.Context, expected Server) error {
	current, e := d.Docker.Inspect(ctx, expected.ID, d.Config.Images)
	if e != nil {
		return e
	}
	if !current.Running || current.Identity != expected.Identity || current.Boot != expected.Boot {
		return errors.New("restart cancelled: identity changed or server stopped")
	}
	w, e := LoadWings(d.Config.WingsConfig)
	if e != nil {
		return e
	}
	return w.Restart(ctx, expected.UUID)
}
func (d *Daemon) snapshot() any {
	type view struct {
		Server    string    `json:"server"`
		Game      string    `json:"game"`
		Running   bool      `json:"running"`
		State     State     `json:"state"`
		Reference Reference `json:"reference"`
		Pending   string    `json:"pending,omitempty"`
	}
	out := []view{}
	for _, s := range d.servers {
		out = append(out, view{s.UUID, s.Identity.Game, s.Running, d.states[s.UUID], d.refs[s.UUID], d.pending[s.UUID].Generation})
	}
	return out
}
func (d *Daemon) String() string { return fmt.Sprintf("sourcenode (%d games)", len(game.All())) }
