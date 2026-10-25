package operations

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/edgegamers/source-egg/internal/fsx"
)

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(b)
}

type zeroReader struct{}

func (zeroReader) Read(b []byte) (int, error) { clear(b); return len(b), nil }
func matches(patterns []string, rel string) bool {
	for cur := rel; cur != "."; cur = path.Dir(cur) {
		for _, p := range patterns {
			if p == "all" {
				return true
			}
			if ok, _ := path.Match(p, cur); ok {
				return true
			}
			if ok, _ := path.Match(p, path.Base(cur)); ok {
				return true
			}
		}
	}
	return false
}
func (m *Manager) wanted(rel string) bool {
	return !protected(rel) && !matches(m.cfg.Backup.Exclude, rel) && (len(m.cfg.Backup.Include) == 0 || matches(m.cfg.Backup.Include, rel))
}
func mutable(rel string) bool {
	for _, p := range strings.Split(strings.ToLower(rel), "/") {
		switch p {
		case "cfg", "addons", "event", "events", "custom", "data", "plugins":
			return true
		}
	}
	return false
}

// Stock files must match both size and contents; a same-size custom file is
// still private data. Configs, addons and event content are always preserved.
func sameStock(ctx context.Context, root, shared *os.Root, rel string, info fs.FileInfo) bool {
	if shared == nil || mutable(rel) || noLinks(shared, rel) != nil {
		return false
	}
	st, e := shared.Lstat(rel)
	if e != nil || !st.Mode().IsRegular() || st.Size() != info.Size() {
		return false
	}
	a, e := root.Open(rel)
	if e != nil {
		return false
	}
	defer a.Close()
	b, e := shared.Open(rel)
	if e != nil {
		return false
	}
	defer b.Close()
	aa, bb := make([]byte, 32768), make([]byte, 32768)
	for {
		if ctx.Err() != nil {
			return false
		}
		na, ea := io.ReadFull(a, aa)
		nb, eb := io.ReadFull(b, bb)
		if na != nb || !bytes.Equal(aa[:na], bb[:nb]) {
			return false
		}
		if ea != nil || eb != nil {
			return (ea == io.EOF || ea == io.ErrUnexpectedEOF) && (eb == io.EOF || eb == io.ErrUnexpectedEOF)
		}
	}
}

type candidate struct {
	name string
	info fs.FileInfo
}

func (m *Manager) candidates(ctx context.Context, root *os.Root, s Server) ([]candidate, int64, error) {
	var shared *os.Root
	if s.SharedRoot != "" {
		var e error
		shared, e = os.OpenRoot(s.SharedRoot)
		if e != nil {
			return nil, 0, e
		}
		defer shared.Close()
	}
	var files []candidate
	var total int64
	budget := m.cfg.Backup.MaxFiles
	e := fsx.BoundedWalk(ctx, root, ".", &budget, func(rel string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if protected(rel) || matches(m.cfg.Backup.Exclude, rel) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !m.wanted(rel) {
			return nil
		}
		if e = noLinks(root, rel); e != nil {
			return e
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if sameStock(ctx, root, shared, rel, info) {
			return nil
		}
		if info.Size() > m.cfg.Backup.MaxBytes-total || len(files) >= m.cfg.Backup.MaxFiles {
			return errors.New("backup exceeds configured file or byte bound")
		}
		total += info.Size()
		files = append(files, candidate{rel, info})
		return nil
	})
	return files, total, e
}
func inside(base, target string) bool {
	r, e := filepath.Rel(base, target)
	return e == nil && (r == "." || filepath.IsLocal(r))
}

func (m *Manager) Backup(ctx context.Context, s Server) (record BackupRecord, err error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	defer func() {
		if err != nil {
			m.notice("backup.failed", s.ID, map[string]any{"error": err.Error()})
		}
	}()
	if e := ctx.Err(); e != nil {
		return record, e
	}
	if e := serverValid(s); e != nil {
		return record, e
	}
	archiveDir := filepath.Join(m.cfg.Backup.Dir, s.ID)
	if inside(s.Volume, archiveDir) || inside(archiveDir, s.Volume) {
		return record, errors.New("backup archive directory must not overlap server volume")
	}
	root, e := os.OpenRoot(s.Volume)
	if e != nil {
		return record, e
	}
	defer root.Close()
	files, raw, e := m.candidates(ctx, root, s)
	if e != nil {
		return record, e
	}
	store, e := secureRoot(m.cfg.Backup.Dir, true)
	if e != nil {
		return record, e
	}
	defer store.Close()
	if e = noLinks(store, s.ID); e != nil {
		return record, e
	}
	if e = store.MkdirAll(s.ID, 0700); e != nil {
		return record, e
	}
	disk, e := m.opt.Disk(m.cfg.Backup.Dir)
	if e != nil {
		return record, e
	}
	overhead := int64(len(files))*1024 + 1<<20
	if raw > disk.Free || overhead > disk.Free-raw || m.cfg.Backup.MinFreeBytes > disk.Free-raw-overhead {
		return record, fmt.Errorf("insufficient backup disk: need %d bytes plus %d reserve, free %d", raw+overhead, m.cfg.Backup.MinFreeBytes, disk.Free)
	}
	now := m.opt.Now()
	name := path.Join(s.ID, now.UTC().Format("20060102T150405.000000000Z")+"-"+fsx.Token()+".tar.gz")
	tmp := name + ".tmp"
	out, e := store.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return record, e
	}
	defer store.Remove(tmp)
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	defer out.Close()
	record = BackupRecord{ServerID: s.ID, Game: s.Game, Time: now, Path: filepath.Join(m.cfg.Backup.Dir, filepath.FromSlash(name))}
	start := time.Now()
	for _, f := range files {
		if e = ctx.Err(); e != nil {
			return record, e
		}
		if e = noLinks(root, f.name); e != nil {
			return record, e
		}
		in, e := root.Open(f.name)
		if e != nil {
			return record, e
		}
		st, e := in.Stat()
		if e != nil || !st.Mode().IsRegular() || !os.SameFile(st, f.info) {
			in.Close()
			return record, fmt.Errorf("backup file changed identity: %s", f.name)
		}
		header, e := tar.FileInfoHeader(f.info, "")
		if e != nil {
			in.Close()
			return record, e
		}
		header.Name = f.name
		header.Mode &= 0777
		if e = tw.WriteHeader(header); e != nil {
			in.Close()
			return record, e
		}
		n, e := io.CopyN(tw, contextReader{ctx, in}, f.info.Size())
		in.Close()
		if errors.Is(e, io.EOF) {
			_, e = io.CopyN(tw, zeroReader{}, f.info.Size()-n)
			record.Changed = append(record.Changed, f.name)
		}
		if e != nil {
			return record, e
		}
		record.Files++
		record.Raw += f.info.Size()
	}
	if e = tw.Close(); e != nil {
		return record, e
	}
	if e = gz.Close(); e != nil {
		return record, e
	}
	if e = out.Sync(); e != nil {
		return record, e
	}
	st, e := out.Stat()
	if e != nil {
		return record, e
	}
	record.Bytes = st.Size()
	record.Seconds = time.Since(start).Seconds()
	if e = out.Close(); e != nil {
		return record, e
	}
	if e = ctx.Err(); e != nil {
		return record, e
	}
	if e = store.Rename(tmp, name); e != nil {
		return record, e
	}
	if e = fsx.JSON(store, name+".json", record, 0600); e != nil {
		store.Remove(name)
		return record, e
	}
	if e = m.retain(ctx, store, s.ID, now); e != nil {
		return record, e
	}
	m.notice("backup.done", s.ID, map[string]any{"files": record.Files, "bytes": record.Bytes})
	return record, nil
}

func (m *Manager) Backups(ctx context.Context, id string) ([]BackupRecord, error) {
	if id != "" && (id == "." || strings.ContainsAny(id, "/\\:\x00")) {
		return nil, errors.New("invalid server ID")
	}
	root, e := secureRoot(m.cfg.Backup.Dir, false)
	if errors.Is(e, os.ErrNotExist) {
		return []BackupRecord{}, nil
	}
	if e != nil {
		return nil, e
	}
	defer root.Close()
	return m.list(ctx, root, id)
}
func (m *Manager) list(ctx context.Context, root *os.Root, id string) ([]BackupRecord, error) {
	var records []BackupRecord
	start := "."
	if id != "" {
		start = id
		if e := noLinks(root, id); e != nil {
			return nil, e
		}
		if _, e := root.Lstat(id); errors.Is(e, os.ErrNotExist) {
			return records, nil
		}
	}
	budget := 100000
	e := fsx.BoundedWalk(ctx, root, start, &budget, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() || !strings.HasSuffix(p, ".tar.gz.json") {
			return nil
		}
		st, e := d.Info()
		if e != nil {
			return e
		}
		if st.Size() > 1<<20 {
			return errors.New("oversized backup metadata")
		}
		b, e := root.ReadFile(p)
		if e != nil {
			return e
		}
		var r BackupRecord
		if json.Unmarshal(b, &r) != nil {
			return fmt.Errorf("invalid backup metadata: %s", p)
		}
		archive := strings.TrimSuffix(p, ".json")
		if !safeRel(archive) || path.Dir(archive) != r.ServerID {
			return errors.New("backup metadata identity mismatch")
		}
		if e = noLinks(root, archive); e != nil {
			return e
		}
		a, e := root.Lstat(archive)
		if e != nil {
			return e
		}
		if !a.Mode().IsRegular() {
			return errors.New("archive is not regular")
		}
		r.Path = filepath.Join(m.cfg.Backup.Dir, filepath.FromSlash(archive))
		records = append(records, r)
		return nil
	})
	sort.Slice(records, func(i, j int) bool {
		if records[i].Time.Equal(records[j].Time) {
			return records[i].Path > records[j].Path
		}
		return records[i].Time.After(records[j].Time)
	})
	return records, e
}
func (m *Manager) retain(ctx context.Context, root *os.Root, id string, now time.Time) error {
	records, e := m.list(ctx, root, id)
	if e != nil {
		return e
	}
	for i, r := range records {
		if (m.cfg.Backup.KeepCount > 0 && i >= m.cfg.Backup.KeepCount) || (m.cfg.Backup.KeepDays > 0 && r.Time.Before(now.AddDate(0, 0, -m.cfg.Backup.KeepDays))) {
			rel, e := filepath.Rel(m.cfg.Backup.Dir, r.Path)
			if e != nil {
				return e
			}
			if e = root.Remove(rel); e != nil {
				return e
			}
			if e = root.Remove(rel + ".json"); e != nil {
				return e
			}
		}
	}
	return nil
}

// Restore validates and stages the entire bounded archive before touching the
// destination, then checks stopped identity again immediately before publishing.
// Publication is atomic per file; unrelated files are never removed.
// Cancellation or an I/O failure during publication can leave earlier files
// restored; this is an overlay operation, not a whole-volume transaction.
func (m *Manager) Restore(ctx context.Context, s Server, archive string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if e := m.stopped(ctx, s); e != nil {
		return e
	}
	rel, e := filepath.Rel(m.cfg.Backup.Dir, archive)
	if e != nil || !safeRel(filepath.ToSlash(rel)) || path.Dir(filepath.ToSlash(rel)) != s.ID || !strings.HasSuffix(rel, ".tar.gz") {
		return errors.New("archive does not belong to server")
	}
	store, e := secureRoot(m.cfg.Backup.Dir, false)
	if e != nil {
		return e
	}
	defer store.Close()
	if e = noLinks(store, filepath.ToSlash(rel)); e != nil {
		return e
	}
	if e = noLinks(store, filepath.ToSlash(rel)+".json"); e != nil {
		return e
	}
	metaFile, e := store.Open(rel + ".json")
	if e != nil {
		return e
	}
	metaBytes, e := io.ReadAll(io.LimitReader(metaFile, (1<<20)+1))
	metaFile.Close()
	if e != nil {
		return e
	}
	if len(metaBytes) > 1<<20 {
		return errors.New("backup metadata too large")
	}
	var meta BackupRecord
	if e = json.Unmarshal(metaBytes, &meta); e != nil {
		return e
	}
	if meta.ServerID != s.ID || meta.Game != s.Game {
		return errors.New("archive server or game identity mismatch")
	}
	f, e := store.Open(rel)
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() || st.Size() > m.cfg.Backup.MaxBytes+int64(m.cfg.Backup.MaxFiles)*1024+1<<20 {
		return errors.New("invalid archive size or type")
	}
	stateRoot, e := secureRoot(m.stateDir, false)
	if e != nil {
		return e
	}
	defer stateRoot.Close()
	stageName := "restore-" + fsx.Token()
	if e = stateRoot.Mkdir(stageName, 0700); e != nil {
		return e
	}
	defer stateRoot.RemoveAll(stageName)
	stage, e := stateRoot.OpenRoot(stageName)
	if e != nil {
		return e
	}
	defer stage.Close()
	gz, e := gzip.NewReader(contextReader{ctx, f})
	if e != nil {
		return e
	}
	defer gz.Close()
	gz.Multistream(false)
	tr := tar.NewReader(io.LimitReader(gz, m.cfg.Backup.MaxBytes+int64(m.cfg.Backup.MaxFiles)*1024+1<<20))
	var names []string
	seen := map[string]bool{}
	var total int64
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		clean := path.Clean(h.Name)
		if !safeRel(h.Name) || clean == "." || h.Name != clean || protected(clean) || seen[clean] {
			return fmt.Errorf("unsafe archive member %q", h.Name)
		}
		seen[clean] = true
		if len(seen) > m.cfg.Backup.MaxFiles {
			return errors.New("restore exceeds member bound")
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return errors.New("archive links and special files are prohibited")
		}
		if h.Size < 0 || h.Size > m.cfg.Backup.MaxBytes-total || len(names) >= m.cfg.Backup.MaxFiles {
			return errors.New("restore exceeds configured limits")
		}
		total += h.Size
		if e = stage.MkdirAll(path.Dir(clean), 0700); e != nil {
			return e
		}
		out, e := stage.OpenFile(clean, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(h.Mode)&0777)
		if e != nil {
			return e
		}
		_, e = io.CopyN(out, contextReader{ctx, tr}, h.Size)
		closeErr := out.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		names = append(names, clean)
	}
	// Drain to verify gzip trailer/checksum before publishing any content.
	if n, err := io.Copy(io.Discard, io.LimitReader(gz, 1025)); err != nil {
		return err
	} else if n > 1024 {
		return errors.New("excess trailing archive data")
	}
	if _, e = io.Copy(io.Discard, io.LimitReader(gz, 1)); e != nil {
		return e
	}
	root, e := os.OpenRoot(s.Volume)
	if e != nil {
		return e
	}
	defer root.Close()
	for _, name := range names {
		if e = noLinks(root, name); e != nil {
			return e
		}
		if st, e := root.Lstat(name); e == nil && !st.Mode().IsRegular() {
			return errors.New("restore destination is not regular")
		}
	}
	if e = m.stopped(ctx, s); e != nil {
		return e
	}
	for _, name := range names {
		if e = ctx.Err(); e != nil {
			return e
		}
		if e = noLinks(root, name); e != nil {
			return e
		}
		in, e := stage.Open(name)
		if e != nil {
			return e
		}
		info, e := in.Stat()
		if e != nil {
			in.Close()
			return e
		}
		e = copyAtomic(ctx, root, name, in, info.Mode(), s.Volume)
		in.Close()
		if e != nil {
			return e
		}
	}
	m.notice("backup.restored", s.ID, map[string]any{"files": len(names)})
	return nil
}

func copyAtomic(ctx context.Context, root *os.Root, name string, in io.Reader, mode fs.FileMode, volume string) error {
	if e := ownedDirectories(root, path.Dir(name)); e != nil {
		return e
	}
	tmp := name + ".restore-" + fsx.Token()
	out, e := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
	if e != nil {
		return e
	}
	defer root.Remove(tmp)
	_, e = io.Copy(out, contextReader{ctx, in})
	if e == nil {
		e = inheritOwner(root, out, name, volume)
	}
	if e == nil {
		e = out.Sync()
	}
	closeErr := out.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	return root.Rename(tmp, name)
}

func ownedDirectories(root *os.Root, dir string) error {
	cur := ""
	for _, part := range strings.Split(dir, "/") {
		cur = path.Join(cur, part)
		if cur == "." {
			continue
		}
		if e := noLinks(root, cur); e != nil {
			return e
		}
		e := root.Mkdir(cur, 0755)
		if errors.Is(e, os.ErrExist) {
			st, err := root.Lstat(cur)
			if err != nil {
				return err
			}
			if !st.IsDir() {
				return errors.New("restore parent is not a directory")
			}
			continue
		}
		if e != nil {
			return e
		}
		if e = inheritDirectoryOwner(root, cur); e != nil {
			return e
		}
	}
	return nil
}
