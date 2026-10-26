package observability

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSweepPreservesArchiveWhenQueuedPreviousDayLogArrivesLate(t *testing.T) {
	c := testConfig(t)
	now := time.Date(2026, 10, 25, 1, 0, 0, 0, time.UTC)
	m, e := New(c, Options{Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	s := server(t)
	previous := now.Add(-2 * time.Hour)
	if e = m.writeLog(context.Background(), logItem{s: s, at: previous, kind: "game", line: "first history"}); e != nil {
		t.Fatal(e)
	}
	if e = m.Sweep(context.Background()); e != nil {
		t.Fatal(e)
	}
	// This item was queued before midnight and processed after the first sweep.
	if e = m.writeLog(context.Background(), logItem{s: s, at: previous, kind: "game", line: "late history"}); e != nil {
		t.Fatal(e)
	}
	if e = m.Sweep(context.Background()); e != nil {
		t.Fatal(e)
	}
	f, e := os.Open(filepath.Join(c.Logs.Dir, s.UUID, "2026-10-24.log.gz"))
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	gz, e := gzip.NewReader(f)
	if e != nil {
		t.Fatal(e)
	}
	defer gz.Close()
	data, e := io.ReadAll(gz)
	if e != nil || strings.Count(string(data), "first history") != 1 || strings.Count(string(data), "late history") != 1 {
		t.Fatalf("history lost or duplicated %q %v", data, e)
	}
}

func TestCrashVolumeWalkBoundsOldFlatDirectoryAndCancellation(t *testing.T) {
	s := server(t)
	for i := 0; i < 300; i++ {
		p := filepath.Join(s.Volume, fmt.Sprintf("old-%03d.log", i))
		os.WriteFile(p, []byte("old"), 0600)
		old := time.Now().Add(-time.Hour)
		os.Chtimes(p, old, old)
	}
	root, e := os.OpenRoot(s.Volume)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	visited := 0
	e = boundedWalk(context.Background(), root, 32, func(_ string, d fs.DirEntry) error {
		visited++
		if st, e := d.Info(); e != nil || !st.ModTime().Before(time.Now().Add(-30*time.Minute)) {
			t.Fatalf("expected old non-evidence file %v", e)
		}
		return nil
	})
	if e != nil || visited != 32 {
		t.Fatalf("walk exceeded global old-file budget visits=%d err=%v", visited, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	visited = 0
	e = boundedWalk(ctx, root, 100000, func(string, fs.DirEntry) error {
		visited++
		if visited == 10 {
			cancel()
		}
		return nil
	})
	if !errors.Is(e, context.Canceled) || visited != 10 {
		t.Fatalf("walk did not cancel between entries visits=%d err=%v", visited, e)
	}
}

func TestSweepPrunesOldCrashOrphansAndKeepsGraceAndUnrelatedFiles(t *testing.T) {
	c := testConfig(t)
	c.Crashes.KeepDays = 0
	c.Crashes.KeepCount = 0
	now := time.Now().UTC()
	m, e := New(c, Options{Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	s := server(t)
	b, e := m.capture(context.Background(), s, CrashReport{Time: now})
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(c.Crashes.Dir, s.UUID)
	old := now.Add(-2 * time.Hour)
	oldNames := []string{"20260101-010101.000000000.tar.gz", "20260101-010102.000000000.tar.gz.tmp", "20260101-010103.000000000.tar.gz.json.tmp", "20260101-010104.000000000.tar.gz.json"}
	for _, name := range oldNames {
		p := filepath.Join(dir, name)
		if e = os.WriteFile(p, []byte("interrupted"), 0600); e != nil {
			t.Fatal(e)
		}
		os.Chtimes(p, old, old)
	}
	fresh := "20260101-010105.000000000.tar.gz.tmp"
	os.WriteFile(filepath.Join(dir, fresh), []byte("fresh"), 0600)
	os.Chtimes(filepath.Join(dir, fresh), now, now)
	unrelated := filepath.Join(dir, "operator-note.tmp")
	os.WriteFile(unrelated, []byte("keep"), 0600)
	os.Chtimes(unrelated, old, old)
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte("untouched"), 0600)
	link := filepath.Join(dir, "20260101-010106.000000000.tar.gz.tmp")
	os.Symlink(outside, link)
	// Sweep shares capture's disk lock and cannot inspect an in-progress write.
	m.disk.Lock()
	done := make(chan error, 1)
	go func() { done <- m.Sweep(context.Background()) }()
	select {
	case e := <-done:
		m.disk.Unlock()
		t.Fatalf("sweep ignored capture lock: %v", e)
	case <-time.After(20 * time.Millisecond):
	}
	m.disk.Unlock()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	for _, name := range oldNames {
		if _, e = os.Stat(filepath.Join(dir, name)); !os.IsNotExist(e) {
			t.Fatalf("old orphan retained %s: %v", name, e)
		}
	}
	for _, name := range []string{fresh, b.Name, b.Name + ".json", "operator-note.tmp"} {
		if _, e = os.Stat(filepath.Join(dir, name)); e != nil {
			t.Fatalf("removed live/grace/unrelated artifact %s: %v", name, e)
		}
	}
	data, e := os.ReadFile(outside)
	if e != nil || string(data) != "untouched" {
		t.Fatalf("followed orphan symlink %q %v", data, e)
	}
}

func TestCrashRejectsVolumeSymlinkAncestorWithoutReadingOutside(t *testing.T) {
	c := testConfig(t)
	now := time.Now().UTC()
	m, e := New(c, Options{Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	outside := t.TempDir()
	volume := filepath.Join(outside, "real-volume")
	os.Mkdir(volume, 0700)
	os.WriteFile(filepath.Join(volume, "secret.log"), []byte("outside evidence"), 0600)
	os.Chtimes(filepath.Join(volume, "secret.log"), now, now)
	base := t.TempDir()
	os.Symlink(outside, filepath.Join(base, "link"))
	s := server(t)
	s.Volume = filepath.Join(base, "link", "real-volume")
	if b, e := m.capture(context.Background(), s, CrashReport{Time: now}); e == nil {
		t.Fatalf("accepted volume ancestor link; read %+v", b.Files)
	}
	bundles, e := m.ListCrashes(context.Background(), s.UUID)
	if e != nil || len(bundles) != 0 {
		t.Fatalf("published outside evidence %+v %v", bundles, e)
	}
	missing := filepath.Join(base, "missing", "real-volume")
	s.Volume = missing
	if _, e = m.capture(context.Background(), s, CrashReport{Time: now}); e == nil {
		t.Fatal("accepted missing volume")
	}
	if _, e = os.Stat(filepath.Join(base, "missing")); !os.IsNotExist(e) {
		t.Fatal("created server volume ancestor")
	}
}
