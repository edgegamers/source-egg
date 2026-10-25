package operations

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"time"

	"github.com/edgegamers/source-egg/internal/fsx"
)

func (m *Manager) Cleanup(ctx context.Context, s Server) (CleanupResult, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	r := CleanupResult{ServerID: s.ID, Time: m.opt.Now(), PerRule: map[string]RuleStat{}}
	if e := ctx.Err(); e != nil {
		return r, e
	}
	if e := serverValid(s); e != nil {
		return r, e
	}
	root, e := os.OpenRoot(s.Volume)
	if e != nil {
		return r, e
	}
	defer root.Close()
	held := map[string]bool{}
	if m.cfg.Cleanup.KeepOpen {
		held, e = m.opt.OpenFiles(ctx, s)
		if e != nil {
			return r, e
		}
	}
	removed := map[string]bool{}
	budget := 100000
	for _, rule := range m.cfg.Cleanup.Rules {
		if rule.Enabled != nil && !*rule.Enabled {
			continue
		}
		for _, raw := range rule.Directories {
			dir := path.Clean(raw)
			if protected(dir) {
				continue
			}
			if e = noLinks(root, dir); e != nil {
				r.Errors = append(r.Errors, e.Error())
				continue
			}
			info, e := root.Lstat(dir)
			if errors.Is(e, os.ErrNotExist) {
				continue
			}
			if e != nil {
				return r, e
			}
			if !info.IsDir() {
				continue
			}
			e = fsx.BoundedWalk(ctx, root, dir, &budget, func(name string, d fs.DirEntry, e error) error {
				if errors.Is(e, os.ErrNotExist) {
					return nil
				}
				if e != nil {
					return e
				}
				if e = ctx.Err(); e != nil {
					return e
				}
				if protected(name) {
					if d.IsDir() {
						return fs.SkipDir
					}
					return nil
				}
				if d.IsDir() {
					if name != dir && rule.Recursive != nil && !*rule.Recursive {
						return fs.SkipDir
					}
					return nil
				}
				if !d.Type().IsRegular() || removed[name] || !matches(rule.Patterns, path.Base(name)) {
					return nil
				}
				st, e := d.Info()
				if e != nil {
					return e
				}
				if rule.Hours > 0 && !st.ModTime().Before(r.Time.Add(-time.Duration(rule.Hours)*time.Hour)) {
					return nil
				}
				if held[name] {
					r.Skipped++
					return nil
				}
				if e = noLinks(root, name); e != nil {
					return e
				}
				targets := []string{name}
				parent := path.Dir(name)
				removeParent := false
				if rule.DeleteParentDir && parent != dir && parent != "." {
					safe := true
					var bundle []string
					e := fsx.BoundedWalk(ctx, root, parent, &budget, func(p string, d fs.DirEntry, e error) error {
						if e != nil {
							return e
						}
						if p == parent {
							return nil
						}
						if d.IsDir() || !d.Type().IsRegular() || held[p] || protected(p) {
							safe = false
							return fs.SkipAll
						}
						bundle = append(bundle, p)
						return nil
					})
					if e != nil {
						return e
					}
					if safe {
						targets = bundle
						removeParent = true
					}
				}
				for _, target := range targets {
					if removed[target] {
						continue
					}
					if e = noLinks(root, target); e != nil {
						return e
					}
					st, e := root.Lstat(target)
					if errors.Is(e, os.ErrNotExist) {
						continue
					}
					if e != nil {
						return e
					}
					if !st.Mode().IsRegular() {
						continue
					}
					if e = root.Remove(target); e != nil {
						return e
					}
					removed[target] = true
					r.Files++
					r.Bytes += st.Size()
					stat := r.PerRule[rule.Name]
					stat.Files++
					stat.Bytes += st.Size()
					r.PerRule[rule.Name] = stat
				}
				if removeParent {
					if e = root.Remove(parent); e != nil && !errors.Is(e, os.ErrNotExist) {
						return e
					}
				}
				return nil
			})
			if e != nil {
				r.Errors = append(r.Errors, e.Error())
				if errors.Is(e, fsx.ErrWalkLimit) || ctx.Err() != nil {
					break
				}
			}
		}
	}
	m.mu.Lock()
	m.state.Cleanup[s.ID] = r
	total := m.state.CleanupTotals[s.ID]
	total.ServerID = s.ID
	total.Time = r.Time
	total.Files += r.Files
	total.Bytes += r.Bytes
	total.Skipped += r.Skipped
	if total.PerRule == nil {
		total.PerRule = map[string]RuleStat{}
	}
	for name, stat := range r.PerRule {
		sum := total.PerRule[name]
		sum.Files += stat.Files
		sum.Bytes += stat.Bytes
		total.PerRule[name] = sum
	}
	m.state.CleanupTotals[s.ID] = total
	m.state.CleanupTotal.Files += r.Files
	m.state.CleanupTotal.Bytes += r.Bytes
	e = m.saveLocked()
	m.mu.Unlock()
	if r.Files > 0 {
		m.notice("cleanup.done", s.ID, map[string]any{"files": r.Files, "bytes": r.Bytes})
	}
	if len(r.Errors) > 0 {
		return r, errors.Join(e, ctx.Err(), errors.New("cleanup encountered errors: "+r.Errors[0]))
	}
	return r, e
}
