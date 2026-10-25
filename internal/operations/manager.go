package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/edgegamers/source-egg/internal/fsx"
)

type Manager struct {
	cfg      Config
	opt      Options
	stateDir string
	mu       sync.Mutex
	opMu     sync.Mutex
	tickMu   sync.Mutex
	state    Status
}

func New(c Config, stateDir string, o Options) (*Manager, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	if e := storagePath(stateDir); e != nil {
		return nil, e
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Disk == nil {
		o.Disk = diskInfo
	}
	if o.OpenFiles == nil {
		o.OpenFiles = processOpenFiles
	}
	if o.RebootRequired == nil {
		o.RebootRequired = func() (bool, error) {
			_, e := os.Stat("/var/run/reboot-required")
			if errors.Is(e, os.ErrNotExist) {
				return false, nil
			}
			return e == nil, e
		}
	}
	m := &Manager{cfg: c, opt: o, stateDir: stateDir, state: Status{Cleanup: map[string]CleanupResult{}}}
	r, e := secureRoot(stateDir, true)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	b, e := r.ReadFile("stats.json")
	if e == nil {
		if len(b) > 8<<20 {
			return nil, errors.New("operations stats too large")
		}
		if e = json.Unmarshal(b, &m.state); e != nil {
			return nil, e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	if m.state.Cleanup == nil {
		m.state.Cleanup = map[string]CleanupResult{}
	}
	if m.state.CleanupTotals == nil {
		m.state.CleanupTotals = map[string]CleanupResult{}
	}
	if m.state.MaintenanceTotals == nil {
		m.state.MaintenanceTotals = map[string]TaskTotal{}
	}
	return m, nil
}
func (m *Manager) saveLocked() error {
	r, e := secureRoot(m.stateDir, false)
	if e != nil {
		return e
	}
	defer r.Close()
	return fsx.JSON(r, "stats.json", m.state, 0600)
}
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, _ := json.Marshal(m.state)
	var s Status
	json.Unmarshal(b, &s)
	return s
}
func (m *Manager) notice(event, id string, fields map[string]any) {
	if m.opt.Notify != nil {
		m.opt.Notify(Notice{Event: event, ServerID: id, Fields: fields})
	}
}
func serverValid(s Server) error {
	if s.ID == "" || s.ID == "." || s.ID == ".." || strings.ContainsAny(s.ID, "/\\:\x00") || !filepath.IsAbs(s.Volume) || filepath.Clean(s.Volume) == string(filepath.Separator) {
		return errors.New("invalid server identity or volume")
	}
	return nil
}
func (m *Manager) stopped(ctx context.Context, s Server) error {
	if e := serverValid(s); e != nil {
		return e
	}
	if s.Running {
		return errors.New("restore requires a stopped server")
	}
	if m.opt.Lookup == nil {
		return errors.New("restore requires authoritative server lookup")
	}
	fresh, e := m.opt.Lookup(ctx, s.ID)
	if e != nil {
		return e
	}
	if fresh.Running || fresh.ID != s.ID || fresh.Game != s.Game || filepath.Clean(fresh.Volume) != filepath.Clean(s.Volume) {
		return errors.New("server started or identity changed")
	}
	return ctx.Err()
}

// noLinks rejects all existing symlink components, even links staying in root.
func noLinks(root *os.Root, rel string) error {
	if !safeRel(rel) {
		return fmt.Errorf("unsafe relative path %q", rel)
	}
	parts := strings.Split(path.Clean(rel), "/")
	cur := ""
	for _, p := range parts {
		cur = path.Join(cur, p)
		st, e := root.Lstat(cur)
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		if e != nil {
			return e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in path %q", cur)
		}
	}
	return nil
}
func protected(rel string) bool {
	for _, p := range strings.Split(path.Clean(rel), "/") {
		switch strings.ToLower(p) {
		case ".source", ".sourcenode", ".steam", "steam", "steamapps", "steamcmd":
			return true
		}
	}
	return false
}

// Tick persists each schedule claim before work, avoiding destructive replays
// after a restart. Failed passes retry at their next normal interval.
func (m *Manager) Tick(ctx context.Context) error {
	m.tickMu.Lock()
	defer m.tickMu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	now := m.opt.Now()
	m.mu.Lock()
	previous := m.state
	b := m.cfg.Backup.Enabled && now.Hour() >= m.cfg.Backup.Hour && (m.state.LastBackup.IsZero() || m.state.LastBackup.In(now.Location()).Format("2006-01-02") != now.Format("2006-01-02"))
	c := m.cfg.Cleanup.Enabled && m.cfg.Cleanup.EveryHours > 0 && (m.state.LastCleanup.IsZero() || now.Sub(m.state.LastCleanup) >= time.Duration(m.cfg.Cleanup.EveryHours)*time.Hour)
	h := m.cfg.Maintenance.Enabled && m.cfg.Maintenance.EveryHours > 0 && (m.state.LastMaintenance.IsZero() || now.Sub(m.state.LastMaintenance) >= time.Duration(m.cfg.Maintenance.EveryHours)*time.Hour)
	w := m.cfg.Maintenance.Enabled && (m.state.LastWatch.IsZero() || now.Sub(m.state.LastWatch) >= time.Hour)
	if b {
		m.state.LastBackup = now
	}
	if c {
		m.state.LastCleanup = now
	}
	if h {
		m.state.LastMaintenance = now
	}
	if w {
		m.state.LastWatch = now
	}
	e := m.saveLocked()
	if e != nil {
		m.state = previous
	}
	m.mu.Unlock()
	if e != nil {
		return e
	}
	var errs []error
	if b || c {
		if m.opt.Servers == nil {
			errs = append(errs, errors.New("server discovery not configured"))
		} else {
			servers, e := m.opt.Servers(ctx)
			if e != nil {
				errs = append(errs, e)
			} else {
				for _, s := range servers {
					if e := ctx.Err(); e != nil {
						return e
					}
					if !s.Running {
						continue
					}
					if b {
						_, e = m.Backup(ctx, s)
						errs = append(errs, e)
					}
					if c {
						_, e = m.Cleanup(ctx, s)
						errs = append(errs, e)
					}
				}
			}
		}
	}
	if h {
		_, e := m.Maintain(ctx, false)
		errs = append(errs, e)
	}
	if w {
		errs = append(errs, m.Watch(ctx))
	}
	return errors.Join(errs...)
}
func (m *Manager) Run(ctx context.Context) error {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := m.Tick(ctx); e != nil {
			m.notice("operations.failed", "", map[string]any{"error": e.Error()})
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
func (m *Manager) OnStart(ctx context.Context, s Server) error {
	if !m.cfg.Cleanup.Enabled || !m.cfg.Cleanup.OnStart {
		return nil
	}
	last := m.Status().Cleanup[s.ID].Time
	if !last.IsZero() && m.opt.Now().Sub(last) < 5*time.Minute {
		return nil
	}
	_, e := m.Cleanup(ctx, s)
	return e
}
