package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	mu              sync.Mutex
	volume, gameDir string
	source          ReleaseSource
	compiler        CompilerProvider
	defaultSource   bool
	notice          func(string)
	manifest        Manifest
	state           managerState
	installer       Installer
}

func New(volume, gameDir string, source ReleaseSource, notice func(string)) (*Manager, error) {
	if !cleanRelative(gameDir, false) || strings.Contains(gameDir, "/") {
		return nil, errors.New("invalid game directory")
	}
	m := &Manager{volume: volume, gameDir: gameDir, source: source, defaultSource: source == nil, notice: notice, state: freshState(), installer: Installer{Volume: volume, GameDir: gameDir}}
	root, e := m.root()
	if e != nil {
		return nil, e
	}
	defer root.Close()
	for _, d := range []string{".source", ".source/plugins", ".source/plugins/packages"} {
		if e = managerPath(root, d, true); e != nil {
			return nil, e
		}
		if e = root.MkdirAll(d, 0700); e != nil {
			return nil, e
		}
		if e = root.Chmod(d, 0700); e != nil {
			return nil, e
		}
	}
	if _, e = root.Lstat(manifestPath); os.IsNotExist(e) {
		if e = managerWrite(root, manifestPath, Manifest{Version: 1, CheckIntervalHours: 6, Plugins: []Plugin{}}); e != nil {
			return nil, e
		}
	} else if e != nil {
		return nil, e
	}
	if e = m.reload(); e != nil {
		return nil, e
	}
	if e = managerRead(root, statePath, &m.state, 16<<20); e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	if m.state.Version != 1 || m.state.Available == nil || m.state.Pending == nil || m.state.Checked == nil {
		return nil, errors.New("invalid plugin manager state")
	}
	for id, s := range m.state.Available {
		if !identifier.MatchString(id) || !validSnapshotReference(s) {
			return nil, errors.New("invalid staged plugin state")
		}
	}
	for id, p := range m.state.Pending {
		for _, name := range p.Preserve {
			if !ValidTarget(name) {
				return nil, errors.New("invalid pending preservation path")
			}
		}
		if !identifier.MatchString(id) || !validSnapshotReference(p.Snapshot) || (p.Kind != "install" && p.Kind != "rollback" && p.Kind != "uninstall") || (p.Automatic && (p.Kind != "install" || p.Replace)) {
			return nil, errors.New("invalid pending plugin action")
		}
	}
	return m, nil
}
func (m *Manager) emit(s string) {
	if m.notice != nil {
		clean := strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return ' '
			}
			return r
		}, s)
		m.notice("SourceEgg | Plugins | " + clean)
	}
}
func (m *Manager) plugin(id string) (Plugin, bool) {
	for _, p := range m.manifest.Plugins {
		if p.ID == id {
			return p, true
		}
	}
	return Plugin{}, false
}

// Startup runs with the game stopped. Only unrecoverable transactions prevent boot.
func (m *Manager) Startup(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.installer.Recover(); e != nil {
		return e
	}
	if e := m.reload(); e != nil {
		m.emit("Configuration error; keeping installed plugins: " + e.Error())
		return nil
	}
	// Discover before applying so successful automatic builds can install on
	// this launch. Explicit pending actions remain present during discovery.
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	m.check(checkCtx, "")
	cancel()
	if err := m.applyPending(); err != nil {
		return err
	}
	m.collect()
	return nil
}

func (m *Manager) applyPending() error {
	ids := make([]string, 0, len(m.state.Pending))
	for id := range m.state.Pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a := m.state.Pending[id]
		p, tracked := m.plugin(id)
		if a.Kind != "uninstall" && (!tracked || a.Recipe != recipeHash(p)) {
			m.emit(id + ": approval cancelled because its recipe changed or was removed")
			delete(m.state.Pending, id)
			continue
		}
		installed, e := m.installer.Installed()
		if e != nil {
			m.emit("Cannot load installed plugin receipts: " + e.Error())
			return nil
		}
		switch a.Kind {
		case "install", "rollback":
			if installed[id].Current.ID != a.Snapshot.ID {
				e = m.installer.Apply(id, a.Snapshot, a.Replace)
			}
		case "uninstall":
			if cur, ok := installed[id]; ok {
				if cur.Current.ID != a.Snapshot.ID {
					e = errors.New("installed version changed; preview uninstall again")
				} else {
					installer := m.installer
					installer.Preserve = map[string][]string{}
					for pluginID, paths := range m.installer.Preserve {
						installer.Preserve[pluginID] = paths
					}
					installer.Preserve[id] = append(append([]string(nil), installer.Preserve[id]...), a.Preserve...)
					e = installer.Uninstall(id)
				}
			}
		}
		if e != nil {
			if errors.Is(e, ErrRecovery) {
				return e
			}
			m.emit(id + ": pending " + a.Kind + " paused: " + e.Error())
			continue
		}
		delete(m.state.Pending, id)
		if a.Kind != "uninstall" && m.state.Available[id].ID == a.Snapshot.ID {
			delete(m.state.Available, id)
		}
		m.emit(id + ": " + a.Kind + " completed")
		if e = m.save(); e != nil {
			m.emit("Cannot save plugin action state: " + e.Error())
			return nil
		}
	}
	if e := m.save(); e != nil {
		m.emit("Cannot save plugin state: " + e.Error())
		return nil
	}
	return nil
}

// Run checks on a short scheduler tick so file-edited intervals take effect.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.mu.Lock()
			if e := m.reload(); e != nil {
				m.emit("Configuration error: " + e.Error())
				m.mu.Unlock()
				continue
			}
			for _, p := range m.manifest.Plugins {
				if time.Since(m.state.Checked[p.ID]) >= time.Duration(m.manifest.CheckIntervalHours)*time.Hour {
					m.check(ctx, p.ID)
				}
			}
			m.mu.Unlock()
		}
	}
}
func (m *Manager) check(ctx context.Context, id string) {
	installed, e := m.installer.Installed()
	if e != nil {
		m.emit("Cannot read installed plugin state: " + e.Error())
		return
	}
	// Queue every already-ready package before network checks can consume the
	// startup budget, including when the configured provider is unavailable.
	for _, p := range m.manifest.Plugins {
		if id != "" && p.ID != id {
			continue
		}
		if snapshot, ok := m.state.Available[p.ID]; ok && snapshot.ID != installed[p.ID].Current.ID {
			m.queueAutomatic(p, snapshot)
		}
	}
	for _, p := range m.manifest.Plugins {
		if ctx.Err() != nil {
			break
		}
		if id != "" && p.ID != id {
			continue
		}
		m.checkPlugin(ctx, p, installed[p.ID].Current)
	}
	if e = m.save(); e != nil {
		m.emit("Cannot persist plugin check: " + e.Error())
		return
	}
	m.collect()
}

func (m *Manager) checkPlugin(ctx context.Context, p Plugin, old Snapshot) {
	parentCtx := ctx
	budget := 2 * time.Minute
	if p.Build != nil {
		budget = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	defer func() {
		if p.Build != nil && parentCtx.Err() != nil {
			delete(m.state.Checked, p.ID) // Retry a startup-budget timeout after the game starts.
		}
	}()
	m.state.Checked[p.ID] = time.Now().UTC()
	release, e := m.source.Resolve(ctx, p)
	if e != nil {
		m.emit(p.ID + ": update check failed; keeping installed files: " + e.Error())
		if cached, ok := m.state.Available[p.ID]; ok && cached.Recipe == recipeHash(p) {
			message := p.ID + ": previously staged version " + cached.Version + " remains available"
			if !m.autoApprove(p) {
				message += "; run egg plugins preview " + p.ID
			}
			m.emit(message)
		}
		return
	}
	if release.Version == "" || len(release.Version) > 500 {
		m.emit(p.ID + ": invalid release version")
		return
	}
	if release.Version == old.Version && old.Recipe == recipeHash(p) {
		delete(m.state.Available, p.ID)
		m.clearAutomatic(p.ID)
		m.emit(p.ID + ": installed " + old.Version + " is current")
		return
	}
	if p.Build != nil {
		if cached, ok := m.state.Available[p.ID]; ok && cached.Version == release.Version && cached.Recipe == recipeHash(p) && m.cachedSourceExists(cached) {
			message := p.ID + ": staged " + cached.Version + " remains available"
			if !m.autoApprove(p) {
				message += "; run egg plugins preview " + p.ID
			}
			m.emit(message)
			return
		}
	}
	snap, e := m.stage(ctx, p, release)
	if e != nil {
		m.emit(p.ID + ": version " + release.Version + " is available, but staging failed; keeping installed files: " + e.Error())
		return
	}
	m.state.Available[p.ID] = snap
	current := old.Version
	if current == "" {
		current = "not managed"
	}
	if m.autoApprove(p) {
		m.emit(p.ID + ": version " + snap.Version + " ready (installed: " + current + ")")
		m.queueAutomatic(p, snap)
	} else {
		m.emit(p.ID + ": new version " + snap.Version + " available (installed: " + current + "); run egg plugins preview " + p.ID)
	}
	if len(old.Files) > 0 && !sameLayout(old, snap) {
		m.emit(p.ID + ": package layout changed; run egg plugins preview " + p.ID + " to inspect mapped paths")
	}
}

func (m *Manager) cachedSourceExists(s Snapshot) bool {
	if !validSnapshotReference(s) {
		return false
	}
	f, err := openInstallFS(m.volume)
	if err != nil {
		return false
	}
	defer f.Close()
	return m.installer.staged(f, s) == nil
}

func sameLayout(a, b Snapshot) bool {
	if len(a.Files) != len(b.Files) {
		return false
	}
	for p := range a.Files {
		if _, ok := b.Files[p]; !ok {
			return false
		}
	}
	return true
}
func (m *Manager) stage(ctx context.Context, p Plugin, r Release) (Snapshot, error) {
	root, e := m.root()
	if e != nil {
		return Snapshot{}, e
	}
	defer root.Close()
	if e = managerPath(root, ".source/plugins/packages", false); e != nil {
		return Snapshot{}, e
	}
	temp, e := os.MkdirTemp(filepath.Join(m.volume, ".source/plugins"), "work-")
	if e != nil {
		return Snapshot{}, e
	}
	defer os.RemoveAll(temp)
	archive := filepath.Join(temp, "download")
	if e = m.source.Download(ctx, p, r, archive); e != nil {
		return Snapshot{}, e
	}
	payload := filepath.Join(temp, "payload")
	var files map[string]File
	if p.Build != nil {
		builder := SourceBuilder{CacheDir: filepath.Join(m.volume, ".source/plugins/toolchains"), Source: m.source, Compiler: m.compiler}
		files, e = builder.Build(ctx, p, r, archive, payload)
	} else {
		files, e = PreparePackage(archive, payload, p, r.Asset.Name)
	}
	if e != nil {
		return Snapshot{}, e
	}
	snap := Snapshot{Version: r.Version, Recipe: recipeHash(p), Files: files}
	b, _ := json.Marshal(snap)
	hash := sha256.Sum256(b)
	snap.ID = hex.EncodeToString(hash[:])
	snap.Directory = ".source/plugins/packages/" + snap.ID
	if !validSnapshotReference(snap) {
		return Snapshot{}, errors.New("empty or invalid mapped package")
	}
	if e = managerPath(root, snap.Directory, true); e != nil {
		return Snapshot{}, e
	}
	if _, e = root.Lstat(snap.Directory); os.IsNotExist(e) {
		e = os.Rename(payload, filepath.Join(m.volume, filepath.FromSlash(snap.Directory)))
		if e != nil {
			return Snapshot{}, e
		}
	} else if e != nil {
		return Snapshot{}, e
	} else if p.Build != nil && !m.cachedSourceExists(snap) {
		// The new payload has the exact original snapshot identity. Restore only
		// those expected bytes; approvals and history still refer to the same ID.
		if e = m.repairSourceSnapshot(snap, payload); e != nil {
			return Snapshot{}, e
		}
	}
	directory, e := root.Open(".source/plugins/packages")
	if e != nil {
		return Snapshot{}, e
	}
	e = directory.Sync()
	directory.Close()
	if e != nil {
		return Snapshot{}, e
	}
	return snap, nil
}

func (m *Manager) repairSourceSnapshot(s Snapshot, payload string) error {
	f, err := openInstallFS(m.volume)
	if err != nil {
		return err
	}
	defer f.Close()
	root, err := os.OpenRoot(payload)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, name := range sortedSnapshotFiles(s.Files) {
		input, err := root.Open(name)
		if err != nil {
			return err
		}
		info, err := input.Stat()
		if err == nil {
			err = f.atomic(s.Directory+"/"+name, input, s.Files[name].SHA256, info.Mode())
		}
		input.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// Keep only available, approved and installed/history packages. Do not touch unknown paths.
func (m *Manager) collect() {
	installed, e := m.installer.Installed()
	if e != nil {
		return
	}
	keep := map[string]bool{}
	for id, s := range m.state.Available {
		if _, ok := m.plugin(id); !ok {
			delete(m.state.Available, id)
			continue
		}
		keep[s.ID] = true
	}
	for _, p := range m.state.Pending {
		keep[p.Snapshot.ID] = true
	}
	for _, i := range installed {
		keep[i.Current.ID] = true
		for _, s := range i.Previous {
			keep[s.ID] = true
		}
	}
	root, e := m.root()
	if e != nil {
		return
	}
	defer root.Close()
	if e = managerPath(root, ".source/plugins/packages", false); e != nil {
		return
	}
	dir, e := root.Open(".source/plugins/packages")
	if e != nil {
		return
	}
	entries, e := dir.ReadDir(-1)
	dir.Close()
	if e != nil {
		return
	}
	for _, entry := range entries {
		id := entry.Name()
		if len(id) != 64 || keep[id] || !entry.IsDir() {
			continue
		}
		if _, e = hex.DecodeString(id); e != nil {
			continue
		}
		_ = root.RemoveAll(".source/plugins/packages/" + id)
	}
	_ = m.save()
}
func Handles(line string) bool { f := strings.Fields(line); return len(f) > 0 && f[0] == "egg" }
