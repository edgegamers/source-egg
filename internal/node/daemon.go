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
	"github.com/edgegamers/source-egg/internal/passwords"
)

type State struct {
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
	prepare          sync.Mutex
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
	pw, e := passwords.Open(filepath.Join(cfg.StateDir, "passwords"), time.Duration(cfg.PasswordIntervalHours)*time.Hour)
	if e != nil {
		return nil, e
	}
	d := &Daemon{Config: cfg, Docker: NewDocker(), Store: &content.Store{Dir: cfg.SharedDir, Installer: &content.Steam{Executable: cfg.SteamCMD, Username: cfg.SteamUsername, Password: cfg.SteamPassword}}, Cache: addons.NewCache(cfg.CacheDir, nil), Passwords: pw, states: map[string]State{}, servers: map[string]Server{}, refs: map[string]Reference{}, pending: map[string]pending{}, socketBoot: map[string]string{}, passwordRestarts: map[string]string{}, listeners: map[string]net.Listener{}}
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
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
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
		d.prepare.Lock()
		defer d.prepare.Unlock()
		out.Managed = d.Config.Shared && s.Env["SOURCE_MODE"] != "standalone"
		if out.Managed {
			g, e := d.desired(s)
			if os.IsNotExist(e) {
				g, e = d.Store.Ensure(ctx, s.Identity)
			}
			if e != nil {
				return out, e
			}
			out.Generation = g
			d.mu.Lock()
			ref := d.refs[s.UUID]
			ref.Pending = g.Key
			d.refs[s.UUID] = ref
			e = d.saveRefs()
			d.mu.Unlock()
			if e != nil {
				return out, e
			}
		}
		if d.Config.FrameworkCache {
			for _, name := range []string{"metamod", "sourcemod"} {
				if !frameworkEnabled(s.Env, name) {
					continue
				}
				pkg, e := d.Cache.Resolve(ctx, name, s.Env[strings.ToUpper(name)+"_VERSION"])
				if e != nil {
					return out, e
				}
				archive, e := d.Cache.Fetch(ctx, pkg)
				if e != nil {
					return out, e
				}
				rel, e := filepath.Rel(d.Config.CacheDir, archive)
				if e != nil || !filepath.IsLocal(rel) {
					return out, errors.New("invalid cache receipt")
				}
				out.Frameworks = append(out.Frameworks, Framework{pkg, rel})
			}
		}
		if s.Env["GENERATE_PASSWORD"] == "1" {
			a, e := d.password(ctx, s)
			if e != nil {
				return out, e
			}
			out.Password = &a
		}
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
		if r.Players < -1 || r.Players > 128 || len(r.Map) > 128 {
			return out, errors.New("invalid server state")
		}
		d.mu.Lock()
		d.states[s.UUID] = State{s.Boot, r.Players, time.Now(), r.Generation, r.Ready}
		p := d.pending[s.UUID]
		d.mu.Unlock()
		if !p.WarnedAt.IsZero() {
			out.RestartSeconds = int(time.Until(p.WarnedAt.Add(time.Duration(d.Config.Restart.WarnSeconds) * time.Second)).Seconds())
			if out.RestartSeconds < 1 {
				out.RestartSeconds = 1
			}
		}
	case "password":
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
	case "crash":
		if len(r.Console) > 64 {
			return out, errors.New("crash tail exceeds limit")
		}
		for _, line := range r.Console {
			if len(line) > 512 {
				return out, errors.New("crash line exceeds limit")
			}
		}
		root, e := os.OpenRoot(d.Config.StateDir)
		if e != nil {
			return out, e
		}
		defer root.Close()
		bundle := "crashes/" + s.UUID + "/" + time.Now().UTC().Format("20060102T150405") + "-" + fsx.Token() + ".json"
		if e = fsx.JSON(root, bundle, map[string]any{"server": s.UUID, "game": s.Identity.Game, "exit_code": r.ExitCode, "console": r.Console}, 0600); e != nil {
			return out, e
		}
		d.notice(ctx, "server.crash", s, map[string]any{"bundle": bundle, "exit_code": r.ExitCode})
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
	if s.Env["GENERATE_PASSWORD"] != "1" {
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
	if d.Config.PasswordWebhook != "" {
		d.deliverPassword(ctx, s, scope, a)
	}
	return a, nil
}
func (d *Daemon) Run(ctx context.Context) error {
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
			continue
		}
	}
	d.mu.Lock()
	d.servers = fresh
	d.mu.Unlock()
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
	if e = BindReadOnly(s.PID, dir, SocketDir); e == nil && d.Config.Shared && s.Env["SOURCE_MODE"] != "standalone" {
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
