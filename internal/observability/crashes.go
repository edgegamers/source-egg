package observability

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Bundle struct {
	UUID       string    `json:"uuid"`
	Name       string    `json:"name"`
	Time       time.Time `json:"time"`
	ExitCode   int       `json:"exit_code"`
	Game       string    `json:"game"`
	ServerName string    `json:"server_name"`
	Map        string    `json:"map"`
	Players    int       `json:"players"`
	Files      []string  `json:"files"`
	Skipped    []string  `json:"skipped"`
	Bytes      int64     `json:"bytes"`
}
type boundedWriter struct {
	w    io.Writer
	left int64
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.left {
		return 0, errors.New("crash bundle byte limit reached")
	}
	n, e := w.w.Write(p)
	w.left -= int64(n)
	return n, e
}
func (m *Manager) capture(ctx context.Context, s Server, cr CrashReport) (Bundle, error) {
	m.disk.Lock()
	defer m.disk.Unlock()
	if e := ctx.Err(); e != nil {
		return Bundle{}, e
	}
	now := m.opts.Now().UTC()
	root, e := privateRoot(m.cfg.Crashes.Dir)
	if e != nil {
		return Bundle{}, e
	}
	defer root.Close()
	r, e := subRoot(root, s.UUID, true)
	if e != nil {
		return Bundle{}, e
	}
	defer r.Close()
	b := Bundle{UUID: s.UUID, Name: now.Format("20060102-150405.000000000") + ".tar.gz", Time: now, ExitCode: cr.ExitCode, Game: s.Game, ServerName: s.Name, Map: s.Map, Players: s.Players, Files: []string{}, Skipped: []string{}}
	tmp := b.Name + ".tmp"
	f, e := r.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY|noFollowFlags, 0600)
	if e != nil {
		return Bundle{}, e
	}
	defer r.Remove(tmp)
	gz := gzip.NewWriter(&boundedWriter{w: f, left: m.cfg.Crashes.MaxBundleBytes})
	tw := tar.NewWriter(gz)
	ok := false
	defer func() {
		if !ok {
			tw.Close()
			gz.Close()
			f.Close()
		}
	}()
	rawLeft := m.cfg.Crashes.MaxBundleBytes - (256 << 10)
	write := func(name, text string) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		data := []byte(text)
		if int64(len(data))+512 > rawLeft {
			return errors.New("crash raw byte limit reached")
		}
		rawLeft -= int64(len(data)) + 512
		if e := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(data)), ModTime: now}); e != nil {
			return e
		}
		_, e := tw.Write(data)
		return e
	}
	if e = write("console.txt", strings.Join(cr.Lines, "\n")+"\n"); e != nil {
		return Bundle{}, e
	}
	info := fmt.Sprintf("uuid: %s\nname: %s\ngame: %s\ntime: %s\nexit_code: %d\nmap: %s\nplayers: %d\n", s.UUID, s.Name, s.Game, now.Format(time.RFC3339), cr.ExitCode, s.Map, s.Players)
	if e = write("info.txt", info); e != nil {
		return Bundle{}, e
	}
	keys := make([]string, 0, len(s.Versions))
	for k := range s.Versions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var versions strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&versions, "%s: %s\n", k, s.Versions[k])
	}
	if e = write("versions.txt", versions.String()); e != nil {
		return Bundle{}, e
	}
	if s.Volume != "" {
		volume, e := directoryRoot(s.Volume, false)
		if e != nil {
			return Bundle{}, e
		}
		defer volume.Close()
		walkErr := boundedWalk(ctx, volume, 100000, func(path string, d fs.DirEntry) error {
			if e := ctx.Err(); e != nil {
				return e
			}
			base := strings.ToLower(d.Name())
			if d.IsDir() {
				if base == "steam" || base == "steamapps" || base == "steamcmd" || base == ".steam" {
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || strings.HasSuffix(base, ".vpk") {
				return nil
			}
			st, e := volume.Lstat(path)
			if e != nil || !st.Mode().IsRegular() || st.ModTime().Before(cr.Time.Add(-2*time.Minute)) || st.ModTime().After(cr.Time.Add(2*time.Minute)) {
				return nil
			}
			if len(b.Files)+len(b.Skipped) >= 256 {
				return fs.SkipAll
			}
			clean := m.clean(filepath.ToSlash(path), 512)
			if st.Size() > MaxCrashFileBytes || st.Size()+512 > rawLeft {
				b.Skipped = append(b.Skipped, clean)
				return nil
			}
			src, e := regularOpen(volume, path)
			if e != nil {
				return nil
			}
			defer src.Close()
			opened, e := src.Stat()
			if e != nil {
				return e
			}
			if opened.Size() != st.Size() {
				b.Skipped = append(b.Skipped, clean)
				return nil
			}
			rawLeft -= st.Size() + 512
			if e = tw.WriteHeader(&tar.Header{Name: "files/" + filepath.ToSlash(path), Mode: 0600, Size: st.Size(), ModTime: st.ModTime()}); e != nil {
				return e
			}
			if _, e = io.CopyN(tw, contextReader{ctx, src}, st.Size()); e != nil {
				return e
			}
			b.Files = append(b.Files, clean)
			return nil
		})
		if walkErr != nil {
			return Bundle{}, walkErr
		}
	}
	if e = write("skipped.txt", strings.Join(b.Skipped, "\n")+"\n"); e != nil {
		return Bundle{}, e
	}
	if e = tw.Close(); e != nil {
		return Bundle{}, e
	}
	if e = gz.Close(); e != nil {
		return Bundle{}, e
	}
	if e = f.Sync(); e != nil {
		return Bundle{}, e
	}
	st, e := f.Stat()
	if e != nil {
		return Bundle{}, e
	}
	b.Bytes = st.Size()
	if e = f.Close(); e != nil {
		return Bundle{}, e
	}
	if e = ctx.Err(); e != nil {
		return Bundle{}, e
	}
	if e = r.Rename(tmp, b.Name); e != nil {
		return Bundle{}, e
	}
	ok = true
	data, e := json.Marshal(b)
	if e == nil {
		e = atomicBytes(r, b.Name+".json", data)
	}
	if e != nil {
		r.Remove(b.Name)
		return Bundle{}, e
	}
	if e = m.pruneServerCrashes(ctx, r, now); e != nil {
		return b, e
	}
	return b, nil
}
func atomicBytes(r *os.Root, name string, data []byte) error {
	tmp := name + ".tmp"
	f, e := r.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY|noFollowFlags, 0600)
	if e != nil {
		return e
	}
	defer r.Remove(tmp)
	_, e = f.Write(data)
	if se := f.Sync(); e == nil {
		e = se
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return r.Rename(tmp, name)
}
func readBundles(ctx context.Context, r *os.Root, id string) ([]Bundle, error) {
	entries, e := readDir(r)
	if e != nil {
		return nil, e
	}
	out := []Bundle{}
	for _, d := range entries {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		if !d.Type().IsRegular() || !strings.HasSuffix(d.Name(), ".tar.gz.json") {
			continue
		}
		f, e := regularOpen(r, d.Name())
		if e != nil {
			continue
		}
		var b Bundle
		e = json.NewDecoder(io.LimitReader(f, 4<<20)).Decode(&b)
		f.Close()
		if e != nil || b.UUID != id || !safeComponent(b.Name) || d.Name() != b.Name+".json" {
			continue
		}
		archive, e := regularOpen(r, b.Name)
		if e != nil {
			continue
		}
		st, e := archive.Stat()
		archive.Close()
		if e != nil {
			continue
		}
		b.Bytes = st.Size()
		b.ServerName = Sanitize(b.ServerName)
		b.Map = Sanitize(b.Map)
		b.Game = Sanitize(b.Game)
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}
func (m *Manager) ListCrashes(ctx context.Context, uuid string) ([]Bundle, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if !m.cfg.Crashes.Enabled {
		return nil, nil
	}
	m.disk.Lock()
	defer m.disk.Unlock()
	root, e := privateRoot(m.cfg.Crashes.Dir)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	out := []Bundle{}
	e = serverRoots(root, uuid, func(id string, r *os.Root) error { b, e := readBundles(ctx, r, id); out = append(out, b...); return e })
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, e
}
func (m *Manager) pruneCrashes(ctx context.Context, now time.Time) error {
	root, e := privateRoot(m.cfg.Crashes.Dir)
	if e != nil {
		return e
	}
	defer root.Close()
	return serverRoots(root, "", func(_ string, r *os.Root) error { return m.pruneServerCrashes(ctx, r, now) })
}
func (m *Manager) pruneServerCrashes(ctx context.Context, r *os.Root, now time.Time) error {
	id := filepath.Base(r.Name())
	bundles, e := readBundles(ctx, r, id)
	if e != nil {
		return e
	}
	retained := map[string]bool{}
	for i, b := range bundles {
		if e := ctx.Err(); e != nil {
			return e
		}
		if m.cfg.Crashes.KeepCount > 0 && i >= m.cfg.Crashes.KeepCount || m.cfg.Crashes.KeepDays > 0 && b.Time.Before(now.AddDate(0, 0, -m.cfg.Crashes.KeepDays)) {
			if e = r.Remove(b.Name); e != nil && !os.IsNotExist(e) {
				return e
			}
			if e = r.Remove(b.Name + ".json"); e != nil && !os.IsNotExist(e) {
				return e
			}
		} else {
			retained[b.Name] = true
			retained[b.Name+".json"] = true
		}
	}
	return pruneCrashOrphans(ctx, r, retained, now)
}

var crashArtifact = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}(?:\.[0-9]{9})?\.tar\.gz(?:\.json)?(?:\.tmp)?$`)

// Capture and Sweep both hold m.disk, so an active publication is never
// collected. A one-hour grace also protects recent interrupted publications.
func pruneCrashOrphans(ctx context.Context, r *os.Root, retained map[string]bool, now time.Time) error {
	f, e := r.Open(".")
	if e != nil {
		return e
	}
	defer f.Close()
	cutoff := now.Add(-time.Hour)
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		entries, readErr := f.ReadDir(128)
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		for _, d := range entries {
			if e := ctx.Err(); e != nil {
				return e
			}
			if retained[d.Name()] || !d.Type().IsRegular() || !crashArtifact.MatchString(d.Name()) {
				continue
			}
			st, e := r.Lstat(d.Name())
			if e != nil {
				if os.IsNotExist(e) {
					continue
				}
				return e
			}
			if !st.Mode().IsRegular() || !st.ModTime().Before(cutoff) {
				continue
			}
			if e = r.Remove(d.Name()); e != nil && !os.IsNotExist(e) {
				return e
			}
		}
		if readErr == io.EOF {
			return nil
		}
	}
}

// Tail returns a sanitized console tail; index 1 selects the newest bundle.
func (m *Manager) Tail(ctx context.Context, uuid string, index int) (string, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	if !idPattern.MatchString(uuid) || index < 1 {
		return "", errors.New("invalid crash selection")
	}
	if !m.cfg.Crashes.Enabled {
		return "", errors.New("crashes disabled")
	}
	m.disk.Lock()
	defer m.disk.Unlock()
	root, e := privateRoot(m.cfg.Crashes.Dir)
	if e != nil {
		return "", e
	}
	defer root.Close()
	r, e := subRoot(root, uuid, false)
	if e != nil {
		return "", e
	}
	defer r.Close()
	bundles, e := readBundles(ctx, r, uuid)
	if e != nil {
		return "", e
	}
	if index > len(bundles) {
		return "", errors.New("crash bundle not found")
	}
	f, e := regularOpen(r, bundles[index-1].Name)
	if e != nil {
		return "", e
	}
	defer f.Close()
	gz, e := gzip.NewReader(f)
	if e != nil {
		return "", e
	}
	defer gz.Close()
	tr := tar.NewReader(io.LimitReader(contextReader{ctx, gz}, m.cfg.Crashes.MaxBundleBytes))
	for {
		h, e := tr.Next()
		if e != nil {
			return "", e
		}
		if h.Name == "console.txt" {
			if h.Typeflag != tar.TypeReg || h.Size > MaxCrashLines*(MaxLineLen+1) {
				return "", errors.New("invalid console archive member")
			}
			data, e := io.ReadAll(io.LimitReader(tr, MaxCrashLines*(MaxLineLen+1)))
			if e != nil {
				return "", e
			}
			return Sanitize(string(data)), nil
		}
	}
}
