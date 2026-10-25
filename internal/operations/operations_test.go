//go:build linux

package operations

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

func put(t *testing.T, base, name, data string) {
	t.Helper()
	p := filepath.Join(base, name)
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(data), 0600); e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T) (*Manager, Server, Config, Options, string) {
	t.Helper()
	base := t.TempDir()
	s := Server{ID: "server-1", Volume: filepath.Join(base, "volume"), SharedRoot: filepath.Join(base, "shared")}
	os.MkdirAll(s.Volume, 0755)
	os.MkdirAll(s.SharedRoot, 0755)
	c := DefaultConfig()
	c.Backup.Dir = filepath.Join(base, "backups")
	o := Options{Now: func() time.Time { return testNow }, Lookup: func(context.Context, string) (Server, error) { return s, nil }, Disk: func(string) (DiskInfo, error) { return DiskInfo{Free: 1 << 40, Total: 1 << 41}, nil }, OpenFiles: func(context.Context, Server) (map[string]bool, error) { return map[string]bool{}, nil }}
	m, e := New(c, filepath.Join(base, "state"), o)
	if e != nil {
		t.Fatal(e)
	}
	return m, s, c, o, base
}

// Catches accidental exclusion of unique event content and inclusion of redistributable files.
func TestBackupRestorePrivateData(t *testing.T) {
	m, s, _, _, _ := fixture(t)
	put(t, s.Volume, "cfg/private.cfg", "secret")
	put(t, s.Volume, "event/special.txt", "unique")
	put(t, s.Volume, "game/base.txt", "shared")
	put(t, s.SharedRoot, "game/base.txt", "shared")
	put(t, s.Volume, "demo.dem", "demo")
	put(t, s.Volume, "steamapps/download", "cache")
	outside := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(outside, []byte("outside"), 0600)
	if e := os.Symlink(outside, filepath.Join(s.Volume, "link")); e != nil {
		t.Fatal(e)
	}
	r, e := m.Backup(context.Background(), s)
	if e != nil {
		t.Fatal(e)
	}
	if r.Files != 2 {
		t.Fatalf("files=%d want 2", r.Files)
	}
	put(t, s.Volume, "cfg/private.cfg", "changed")
	put(t, s.Volume, "unrelated", "keep")
	if e = m.Restore(context.Background(), s, r.Path); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(s.Volume, "cfg/private.cfg"))
	if string(b) != "secret" {
		t.Fatalf("restore=%q", b)
	}
	b, _ = os.ReadFile(filepath.Join(s.Volume, "unrelated"))
	if string(b) != "keep" {
		t.Fatal("unrelated removed")
	}
	rs, e := m.Backups(context.Background(), s.ID)
	if e != nil || len(rs) != 1 {
		t.Fatalf("list %v %v", rs, e)
	}
}

func TestLowDiskPublishesNothing(t *testing.T) {
	_, s, c, o, base := fixture(t)
	put(t, s.Volume, "cfg", "payload")
	o.Disk = func(string) (DiskInfo, error) { return DiskInfo{Free: c.Backup.MinFreeBytes, Total: 1 << 40}, nil }
	m, _ := New(c, filepath.Join(base, "other"), o)
	if _, e := m.Backup(context.Background(), s); e == nil {
		t.Fatal("low disk accepted")
	}
	rs, _ := m.Backups(context.Background(), s.ID)
	if len(rs) != 0 {
		t.Fatal("published partial backup")
	}
}

func archive(t *testing.T, m *Manager, s Server, headers []*tar.Header) string {
	t.Helper()
	p := filepath.Join(m.cfg.Backup.Dir, s.ID, "hostile.tar.gz")
	os.MkdirAll(filepath.Dir(p), 0700)
	f, _ := os.Create(p)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, h := range headers {
		if e := tw.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if h.Size > 0 {
			io.CopyN(tw, strings.NewReader(strings.Repeat("x", int(h.Size))), h.Size)
		}
	}
	tw.Close()
	gz.Close()
	f.Close()
	meta, _ := json.Marshal(BackupRecord{ServerID: s.ID, Game: s.Game, Path: p})
	os.WriteFile(p+".json", meta, 0600)
	return p
}

func TestRestoreRejectsUnsafeArchiveBeforeAnyWrite(t *testing.T) {
	for _, h := range []*tar.Header{{Name: "../escape", Mode: 0600, Size: 1}, {Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/tmp"}, {Name: "hard", Typeflag: tar.TypeLink, Linkname: "cfg"}, {Name: "/absolute", Size: 1}, {Name: "C:\\escape", Size: 1}} {
		t.Run(h.Name, func(t *testing.T) {
			m, s, _, _, _ := fixture(t)
			p := archive(t, m, s, []*tar.Header{{Name: "first", Mode: 0600, Size: 1}, h})
			if e := m.Restore(context.Background(), s, p); e == nil {
				t.Fatal("unsafe archive accepted")
			}
			if _, e := os.Stat(filepath.Join(s.Volume, "first")); !os.IsNotExist(e) {
				t.Fatal("partial restore on invalid archive")
			}
		})
	}
}

func TestRestoreRechecksStoppedAndRejectsDestinationLinks(t *testing.T) {
	_, s, c, o, base := fixture(t)
	calls := 0
	o.Lookup = func(context.Context, string) (Server, error) {
		calls++
		fresh := s
		if calls > 1 {
			fresh.Running = true
		}
		return fresh, nil
	}
	m, _ := New(c, filepath.Join(base, "recheck"), o)
	p := archive(t, m, s, []*tar.Header{{Name: "cfg", Size: 1}})
	if e := m.Restore(context.Background(), s, p); e == nil {
		t.Fatal("started server accepted")
	}
	if _, e := os.Stat(filepath.Join(s.Volume, "cfg")); !os.IsNotExist(e) {
		t.Fatal("wrote running server")
	}
	o.Lookup = func(context.Context, string) (Server, error) { return s, nil }
	m, _ = New(c, filepath.Join(base, "links"), o)
	outside := t.TempDir()
	put(t, outside, "secret", "untouched")
	os.Symlink(outside, filepath.Join(s.Volume, "cfgdir"))
	p = archive(t, m, s, []*tar.Header{{Name: "cfgdir/secret", Size: 1}})
	if e := m.Restore(context.Background(), s, p); e == nil {
		t.Fatal("destination symlink accepted")
	}
	b, _ := os.ReadFile(filepath.Join(outside, "secret"))
	if string(b) != "untouched" {
		t.Fatal("outside changed")
	}
}

func TestBackupBoundsRetentionAndCancellation(t *testing.T) {
	_, s, c, o, base := fixture(t)
	c.Backup.KeepCount = 1
	stamp := testNow
	o.Now = func() time.Time { return stamp }
	m, _ := New(c, filepath.Join(base, "retention"), o)
	put(t, s.Volume, "cfg", "abc")
	if _, e := m.Backup(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	stamp = stamp.Add(time.Hour)
	if _, e := m.Backup(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	rs, _ := m.Backups(context.Background(), s.ID)
	if len(rs) != 1 {
		t.Fatalf("retention=%d", len(rs))
	}
	c.Backup.MaxBytes = 2
	m, _ = New(c, filepath.Join(base, "bounds"), o)
	if _, e := m.Backup(context.Background(), s); e == nil {
		t.Fatal("oversize accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := m.Backup(ctx, s); !errors.Is(e, context.Canceled) {
		t.Fatalf("cancel=%v", e)
	}
}

func TestCleanupAgeOpenSymlinkAndPersistedStats(t *testing.T) {
	_, s, c, o, base := fixture(t)
	c.Cleanup.Rules = []Rule{{Name: "demos", Directories: []string{"."}, Patterns: []string{"*.dem"}, Hours: 24}}
	put(t, s.Volume, "old.dem", "old")
	put(t, s.Volume, "held.dem", "held")
	put(t, s.Volume, "fresh.dem", "fresh")
	old := testNow.Add(-48 * time.Hour)
	os.Chtimes(filepath.Join(s.Volume, "old.dem"), old, old)
	os.Chtimes(filepath.Join(s.Volume, "held.dem"), old, old)
	os.Chtimes(filepath.Join(s.Volume, "fresh.dem"), testNow, testNow)
	outside := t.TempDir()
	put(t, outside, "outside.dem", "outside")
	os.Symlink(outside, filepath.Join(s.Volume, "link"))
	o.OpenFiles = func(context.Context, Server) (map[string]bool, error) { return map[string]bool{"held.dem": true}, nil }
	state := filepath.Join(base, "cleanup-state")
	m, _ := New(c, state, o)
	r, e := m.Cleanup(context.Background(), s)
	if e != nil {
		t.Fatal(e)
	}
	if r.Files != 1 || r.Bytes != 3 || r.Skipped != 1 {
		t.Fatalf("result=%+v", r)
	}
	for _, n := range []string{"held.dem", "fresh.dem"} {
		if _, e := os.Stat(filepath.Join(s.Volume, n)); e != nil {
			t.Fatal(e)
		}
	}
	m, e = New(c, state, o)
	if e != nil || m.Status().Cleanup[s.ID].Files != 1 {
		t.Fatalf("stats %v %+v", e, m.Status())
	}
	if _, e := os.Stat(filepath.Join(outside, "outside.dem")); e != nil {
		t.Fatal(e)
	}
}

func TestCleanupBundleDoesNotDeleteOpenSiblingOrNestedDirectory(t *testing.T) {
	_, s, c, o, base := fixture(t)
	c.Cleanup.Rules = []Rule{{Name: "crashes", Directories: []string{"crashes"}, Patterns: []string{"*.dmp"}, DeleteParentDir: true}}
	for _, n := range []string{"crashes/root.dmp", "crashes/held/a.dmp", "crashes/held/open.log", "crashes/nested/a.dmp", "crashes/nested/sub/keep", "crashes/safe/a.dmp", "crashes/safe/log"} {
		put(t, s.Volume, n, "x")
	}
	o.OpenFiles = func(context.Context, Server) (map[string]bool, error) {
		return map[string]bool{"crashes/held/open.log": true}, nil
	}
	m, _ := New(c, filepath.Join(base, "bundles"), o)
	if _, e := m.Cleanup(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	for _, n := range []string{"crashes", "crashes/held/open.log", "crashes/nested/sub/keep"} {
		if _, e := os.Stat(filepath.Join(s.Volume, n)); e != nil {
			t.Fatalf("lost %s: %v", n, e)
		}
	}
	if _, e := os.Stat(filepath.Join(s.Volume, "crashes/safe")); !os.IsNotExist(e) {
		t.Fatal("safe bundle retained")
	}
}

func TestMaintenanceDryRunSafeArgumentsAndPersistence(t *testing.T) {
	_, _, c, o, base := fixture(t)
	var commands []string
	o.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		if name == "journalctl" {
			return []byte("Archived and active journals take up 1.0M in the file system."), nil
		}
		return []byte(`{"SpaceReclaimed":123,"ImagesDeleted":[{}]}`), nil
	}
	m, _ := New(c, filepath.Join(base, "maint"), o)
	if _, e := m.Maintain(context.Background(), false); e != nil {
		t.Fatal(e)
	}
	for _, cmd := range commands {
		if strings.Contains(cmd, "prune") || strings.Contains(cmd, "vacuum") {
			t.Fatalf("dry-run mutation: %s", cmd)
		}
	}
	commands = nil
	if _, e := m.Maintain(context.Background(), true); e != nil {
		t.Fatal(e)
	}
	want := []string{"docker image prune --force --filter dangling=true", "docker builder prune --force --filter until=168h", "journalctl --disk-usage", "journalctl --vacuum-time=14d", "journalctl --disk-usage"}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands=%v", commands)
	}
	m, e := New(c, filepath.Join(base, "maint"), o)
	if e != nil || m.Status().MaintenanceRuns != 1 {
		t.Fatalf("maintenance stats %v %+v", e, m.Status())
	}
}

func TestWatchDedupRecoveryAndScheduleSurviveRestart(t *testing.T) {
	_, s, c, o, base := fixture(t)
	s.Running = true
	c.Backup.Enabled = true
	c.Backup.Hour = 5
	c.Maintenance.Enabled = true
	stamp := testNow
	low, reboot := true, true
	var notices []string
	o.Now = func() time.Time { return stamp }
	o.Disk = func(string) (DiskInfo, error) {
		if low {
			return DiskInfo{Free: 1, Total: 100}, nil
		}
		return DiskInfo{Free: 1 << 40, Total: 1 << 41}, nil
	}
	o.RebootRequired = func() (bool, error) { return reboot, nil }
	o.Notify = func(n Notice) { notices = append(notices, n.Event) }
	o.Servers = func(context.Context) ([]Server, error) { return []Server{s}, nil }
	state := filepath.Join(base, "schedule")
	m, _ := New(c, state, o)
	m.Watch(context.Background())
	m.Watch(context.Background())
	m, _ = New(c, state, o)
	m.Watch(context.Background())
	if len(notices) != 2 {
		t.Fatalf("dedup %v", notices)
	}
	low, reboot = false, false
	m.Watch(context.Background())
	low, reboot = true, true
	m.Watch(context.Background())
	if len(notices) != 4 {
		t.Fatalf("recovery %v", notices)
	}
	low = false
	stamp = stamp.Add(24 * time.Hour)
	m.Tick(context.Background())
	rs, _ := m.Backups(context.Background(), s.ID)
	if len(rs) != 1 {
		t.Fatalf("scheduled backups=%d", len(rs))
	}
	m, _ = New(c, state, o)
	m.Tick(context.Background())
	rs, _ = m.Backups(context.Background(), s.ID)
	if len(rs) != 1 {
		t.Fatal("repeated scheduled backup")
	}
}

func TestValidationRejectsEscapesAndUnsafeLimits(t *testing.T) {
	for _, mutate := range []func(*Config){func(c *Config) {
		c.Cleanup.Rules = []Rule{{Name: "bad", Directories: []string{"../escape"}, Patterns: []string{"*"}}}
	}, func(c *Config) { c.Backup.Include = []string{"/etc"} }, func(c *Config) { c.Backup.MaxBytes = -1 }, func(c *Config) { c.Maintenance.JournalDays = 0 }, func(c *Config) { c.Backup.Hour = 24 }} {
		c := DefaultConfig()
		mutate(&c)
		if e := c.Validate(); e == nil {
			t.Fatal("unsafe config accepted")
		}
	}
}

func TestSetupRequiresExplicitHostWriterAndDisablesReboot(t *testing.T) {
	_, _, c, o, base := fixture(t)
	var wrote bool
	o.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "dpkg-query" {
			t.Fatalf("unexpected %s", name)
		}
		return []byte("install ok installed"), nil
	}
	o.WriteHostFile = func(_ context.Context, p string, b []byte, mode os.FileMode) error {
		if p != "/etc/apt/apt.conf.d/52sourcenode-unattended-upgrades" || mode != 0644 || !strings.Contains(string(b), `Automatic-Reboot "false"`) {
			t.Fatalf("unsafe setup %s %s", p, b)
		}
		wrote = true
		return nil
	}
	m, _ := New(c, filepath.Join(base, "setup"), o)
	if e := m.Setup(context.Background()); e != nil || !wrote {
		t.Fatalf("setup %v", e)
	}
}

// Catches root-owned restored directories that prevent the game writing data.
func TestRestoreNewDirectoriesInheritVolumeOwner(t *testing.T) {
	m, s, _, _, _ := fixture(t)
	if err := os.Chown(s.Volume, 1234, 1234); err != nil {
		t.Fatal(err)
	}
	p := archive(t, m, s, []*tar.Header{{Name: "addons/plugin/data.cfg", Mode: 0600, Size: 1}})
	if err := m.Restore(context.Background(), s, p); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(s.Volume)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, name := range []string{"addons", "addons/plugin", "addons/plugin/data.cfg"} {
		info, err := root.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if uid, gid := ownerIDs(info); uid != 1234 || gid != 1234 {
			t.Fatalf("%s owner=%d:%d", name, uid, gid)
		}
	}
}

func TestBackupPreservesSameSizeCustomAndProtectsReceipts(t *testing.T) {
	m, s, _, _, _ := fixture(t)
	put(t, s.Volume, "game/base.txt", "custom")
	put(t, s.SharedRoot, "game/base.txt", "stock!")
	put(t, s.Volume, "cfg/same.cfg", "keep")
	put(t, s.SharedRoot, "cfg/same.cfg", "keep")
	put(t, s.Volume, ".source/credentials", "secret")
	put(t, s.Volume, "egg/.source/receipt", "secret")
	r, e := m.Backup(context.Background(), s)
	if e != nil {
		t.Fatal(e)
	}
	if r.Files != 2 {
		t.Fatalf("files=%d want only private custom/config files", r.Files)
	}
}

func TestRestoreRejectsCorruptTrailerWithoutWriting(t *testing.T) {
	m, s, _, _, _ := fixture(t)
	p := archive(t, m, s, []*tar.Header{{Name: "cfg", Mode: 0600, Size: 1}})
	b, _ := os.ReadFile(p)
	b[len(b)-8] ^= 255
	os.WriteFile(p, b, 0600)
	if e := m.Restore(context.Background(), s, p); e == nil {
		t.Fatal("corrupt archive accepted")
	}
	if _, e := os.Stat(filepath.Join(s.Volume, "cfg")); !os.IsNotExist(e) {
		t.Fatal("corrupt archive wrote data")
	}
}

func TestCleanupRefusesUninspectableOpenFiles(t *testing.T) {
	_, s, c, o, base := fixture(t)
	c.Cleanup.Rules = []Rule{{Name: "dumps", Directories: []string{"."}, Patterns: []string{"*.dmp"}}}
	put(t, s.Volume, "held.dmp", "keep")
	o.OpenFiles = func(context.Context, Server) (map[string]bool, error) { return nil, os.ErrPermission }
	m, _ := New(c, filepath.Join(base, "closed"), o)
	if _, e := m.Cleanup(context.Background(), s); e == nil {
		t.Fatal("unsafe cleanup accepted")
	}
	if _, e := os.Stat(filepath.Join(s.Volume, "held.dmp")); e != nil {
		t.Fatal("open file lost")
	}
}

func TestMaintenanceReclaimedBytesAndErrors(t *testing.T) {
	_, _, c, o, base := fixture(t)
	c.Maintenance.Journal = false
	o.Command = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "image" {
			return []byte("Deleted: sha256:abc\nTotal reclaimed space: 1.5MB\n"), nil
		}
		return []byte("Total reclaimed space: 2kB\n"), nil
	}
	m, _ := New(c, filepath.Join(base, "bytes"), o)
	rs, e := m.Maintain(context.Background(), true)
	if e != nil {
		t.Fatal(e)
	}
	if len(rs) != 2 || rs[0].Items != 1 || rs[0].Bytes != 1500000 || rs[1].Bytes != 2000 || m.Status().MaintenanceBytes != 1502000 {
		t.Fatalf("results=%+v status=%+v", rs, m.Status())
	}
}

func TestCleanupTotalsAccumulateAcrossRestarts(t *testing.T) {
	_, s, c, o, base := fixture(t)
	c.Cleanup.Rules = []Rule{{Name: "demos", Directories: []string{"."}, Patterns: []string{"*.dem"}}}
	state := filepath.Join(base, "totals")
	m, _ := New(c, state, o)
	put(t, s.Volume, "one.dem", "abc")
	m.Cleanup(context.Background(), s)
	m, _ = New(c, state, o)
	put(t, s.Volume, "two.dem", "hello")
	m.Cleanup(context.Background(), s)
	r := m.Status().CleanupTotals[s.ID]
	if r.Files != 2 || r.Bytes != 8 || r.PerRule["demos"].Files != 2 {
		t.Fatalf("totals=%+v", r)
	}
}

func TestRestoreBoundsDirectoryMembersAndTrailingData(t *testing.T) {
	for _, kind := range []string{"directories", "trailing"} {
		t.Run(kind, func(t *testing.T) {
			_, s, c, o, base := fixture(t)
			c.Backup.MaxFiles = 2
			m, _ := New(c, filepath.Join(base, "bounded"), o)
			var p string
			if kind == "directories" {
				p = archive(t, m, s, []*tar.Header{{Name: "a", Typeflag: tar.TypeDir}, {Name: "b", Typeflag: tar.TypeDir}, {Name: "c", Typeflag: tar.TypeDir}})
			} else {
				p = archive(t, m, s, []*tar.Header{{Name: "file", Size: 1}})
				f, _ := os.Open(p)
				gz, _ := gzip.NewReader(f)
				data, _ := io.ReadAll(gz)
				gz.Close()
				f.Close()
				f, _ = os.Create(p)
				w := gzip.NewWriter(f)
				w.Write(data)
				w.Write(make([]byte, 4096))
				w.Close()
				f.Close()
			}
			if e := m.Restore(context.Background(), s, p); e == nil {
				t.Fatal("unbounded archive accepted")
			}
		})
	}
}

func TestBackupEntryBoundCountsExcludedFiles(t *testing.T) {
	m, s, _, _, _ := fixture(t)
	m.cfg.Backup.MaxFiles = 1
	for i := 0; i < 300; i++ {
		put(t, s.Volume, fmt.Sprintf("excluded-%03d.dem", i), "demo")
	}
	if _, e := m.Backup(context.Background(), s); e == nil {
		t.Fatal("excluded entries bypassed backup max_files")
	}
	records, e := m.Backups(context.Background(), s.ID)
	if e != nil || len(records) != 0 {
		t.Fatalf("partial backup published: %v %v", records, e)
	}
}

func TestOnStartDebounceAndDisabledSchedules(t *testing.T) {
	_, s, c, o, base := fixture(t)
	c.Cleanup.Enabled = true
	c.Cleanup.Rules = []Rule{{Name: "demo", Directories: []string{"."}, Patterns: []string{"*.dem"}}}
	state := filepath.Join(base, "debounce")
	m, _ := New(c, state, o)
	put(t, s.Volume, "old.dem", "x")
	if e := m.OnStart(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	m, _ = New(c, state, o)
	put(t, s.Volume, "new.dem", "x")
	if e := m.OnStart(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(s.Volume, "new.dem")); e != nil {
		t.Fatal("repeated on-start cleanup")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := m.Run(ctx); !errors.Is(e, context.Canceled) {
		t.Fatalf("run cancellation=%v", e)
	}
}

func TestOperationRootsRejectSystemDirectoriesAndSymlinkAncestors(t *testing.T) {
	for _, dir := range []string{"/", "/etc", "/tmp/..", "/var", "/var/lib", "/etc/backups"} {
		c := DefaultConfig()
		c.Backup.Dir = dir
		if c.Validate() == nil {
			t.Fatalf("unsafe backup dir %q", dir)
		}
	}
	_, s, c, o, base := fixture(t)
	target := t.TempDir()
	link := filepath.Join(base, "linked")
	if e := os.Symlink(target, link); e != nil {
		t.Fatal(e)
	}
	if _, e := New(c, filepath.Join(link, "state"), o); e == nil {
		t.Fatal("symlink state root accepted")
	}
	c.Backup.Dir = filepath.Join(link, "archives")
	m, e := New(c, filepath.Join(base, "safe-state"), o)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Backup(context.Background(), s); e == nil {
		t.Fatal("symlink backup ancestor accepted")
	}
	entries, _ := os.ReadDir(target)
	if len(entries) != 0 {
		t.Fatal("mutated symlink target")
	}
}

func TestRestoreRejectsArchiveFromPreviousGame(t *testing.T) {
	_, s, c, o, base := fixture(t)
	s.Game = "css"
	o.Lookup = func(context.Context, string) (Server, error) { return s, nil }
	m, _ := New(c, filepath.Join(base, "game"), o)
	put(t, s.Volume, "cfg/settings", "old-game")
	record, e := m.Backup(context.Background(), s)
	if e != nil {
		t.Fatal(e)
	}
	s.Game = "tf2"
	put(t, s.Volume, "cfg/settings", "new-game")
	if e = m.Restore(context.Background(), s, record.Path); e == nil {
		t.Fatal("previous game archive accepted")
	}
	b, _ := os.ReadFile(filepath.Join(s.Volume, "cfg/settings"))
	if string(b) != "new-game" {
		t.Fatal("wrong game restore changed volume")
	}
}

func TestCleanupContainerDescriptorsProtectMatchingVolumeFiles(t *testing.T) {
	_, s, c, o, base := fixture(t)
	s.PID = 99
	s.Running = true
	c.Cleanup.Rules = []Rule{{Name: "demos", Directories: []string{"."}, Patterns: []string{"*.dem"}}}
	put(t, s.Volume, "demos/held.dem", "held")
	put(t, s.Volume, "demos/host-held.dem", "held")
	put(t, s.Volume, "free.dem", "free")
	put(t, s.Volume, "outside.dem", "free")
	proc := filepath.Join(base, "proc")
	fd := filepath.Join(proc, "99/fd")
	if e := os.MkdirAll(fd, 0700); e != nil {
		t.Fatal(e)
	}
	for number, target := range map[string]string{"1": "/home/container/demos/held.dem", "2": filepath.Join(s.Volume, "demos/host-held.dem"), "3": "/home/container/../outside.dem", "4": "/home/container-neighbour/outside.dem", "5": "socket:[42]", "6": "/home/container/link/outside.dem"} {
		if e := os.Symlink(target, filepath.Join(fd, number)); e != nil {
			t.Fatal(e)
		}
	}
	outside := t.TempDir()
	put(t, outside, "outside.dem", "outside")
	if e := os.Symlink(outside, filepath.Join(s.Volume, "link")); e != nil {
		t.Fatal(e)
	}
	o.OpenFiles = func(ctx context.Context, server Server) (map[string]bool, error) {
		return processOpenFilesAt(ctx, server, proc)
	}
	m, e := New(c, filepath.Join(base, "container-open"), o)
	if e != nil {
		t.Fatal(e)
	}
	r, e := m.Cleanup(context.Background(), s)
	if e != nil {
		t.Fatal(e)
	}
	if r.Files != 2 || r.Skipped != 2 {
		t.Fatalf("cleanup=%+v", r)
	}
	for _, name := range []string{"demos/held.dem", "demos/host-held.dem"} {
		if _, e := os.Stat(filepath.Join(s.Volume, name)); e != nil {
			t.Fatalf("lost open file %s", name)
		}
	}
	if b, e := os.ReadFile(filepath.Join(outside, "outside.dem")); e != nil || string(b) != "outside" {
		t.Fatal("outside descriptor target modified")
	}
}

func TestProcessOpenFilesReadsRealDescriptorWithoutFollowingIt(t *testing.T) {
	_, s, _, _, _ := fixture(t)
	put(t, s.Volume, "open.dem", "held")
	file, e := os.Open(filepath.Join(s.Volume, "open.dem"))
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	s.PID = os.Getpid()
	s.Running = true
	held, e := processOpenFiles(context.Background(), s)
	if e != nil {
		t.Fatal(e)
	}
	if !held["open.dem"] {
		t.Fatalf("real descriptor absent: %v", held)
	}
}

func TestMaintenanceTaskTotalsPersistExcludeDryAndFailedTasks(t *testing.T) {
	_, _, c, o, base := fixture(t)
	c.Maintenance.Journal = false
	fail := false
	o.Command = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) > 1 && args[1] == "ls" {
			return []byte("sha256:dry"), nil
		}
		if args[0] == "image" {
			if fail {
				return []byte("Deleted: partial\nTotal reclaimed space: 50MB"), errors.New("engine unavailable")
			}
			return []byte("Deleted: sha256:one\nTotal reclaimed space: 1MB"), nil
		}
		return []byte("Total reclaimed space: 2kB"), nil
	}
	state := filepath.Join(base, "task-totals")
	m, e := New(c, state, o)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Maintain(context.Background(), false); e != nil {
		t.Fatal(e)
	}
	if len(m.Status().MaintenanceTotals) != 0 {
		t.Fatal("dry run increased totals")
	}
	if _, e = m.Maintain(context.Background(), true); e != nil {
		t.Fatal(e)
	}
	m, e = New(c, state, o)
	if e != nil {
		t.Fatal(e)
	}
	fail = true
	if _, e = m.Maintain(context.Background(), true); e == nil {
		t.Fatal("maintenance command error lost")
	}
	m, e = New(c, state, o)
	if e != nil {
		t.Fatal(e)
	}
	status := m.Status()
	images := status.MaintenanceTotals["images"]
	cache := status.MaintenanceTotals["build-cache"]
	if images.Runs != 1 || images.Items != 1 || images.Bytes != 1000000 || cache.Runs != 2 || cache.Bytes != 4000 {
		t.Fatalf("task totals=%+v", status.MaintenanceTotals)
	}
	if status.MaintenanceBytes != 1004000 {
		t.Fatalf("global bytes=%d", status.MaintenanceBytes)
	}
}

func TestTickRetriesClaimsAfterTransientPersistenceFailure(t *testing.T) {
	_, s, c, o, base := fixture(t)
	s.Running = true
	c.Backup.Enabled = true
	c.Cleanup.Enabled = true
	c.Maintenance.Enabled = true
	c.Maintenance.Images = false
	c.Maintenance.BuildCache = false
	c.Maintenance.Journal = false
	o.RebootRequired = func() (bool, error) { return false, nil }
	discovered := 0
	o.Servers = func(context.Context) ([]Server, error) { discovered++; return []Server{s}, nil }
	state := filepath.Join(base, "claim-retry")
	m, e := New(c, state, o)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(filepath.Join(state, "stats.json"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = m.Tick(context.Background()); e == nil {
		t.Fatal("persist failure ignored")
	}
	status := m.Status()
	if !status.LastBackup.IsZero() || !status.LastCleanup.IsZero() || !status.LastMaintenance.IsZero() || !status.LastWatch.IsZero() {
		t.Fatalf("failed persistence consumed schedule claims: %+v", status)
	}
	if discovered != 0 {
		t.Fatal("performed work before persisting schedule")
	}
	if e = os.Remove(filepath.Join(state, "stats.json")); e != nil {
		t.Fatal(e)
	}
	if e = m.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	records, e := m.Backups(context.Background(), s.ID)
	if e != nil || len(records) != 1 || discovered != 1 {
		t.Fatalf("retry did not run scheduled work: records=%d discovery=%d error=%v", len(records), discovered, e)
	}
	if e = m.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	if discovered != 1 {
		t.Fatal("successful claims replayed")
	}
}

func TestWatchRetriesNoticesAfterTransientPersistenceFailure(t *testing.T) {
	_, _, c, o, base := fixture(t)
	o.Disk = func(string) (DiskInfo, error) { return DiskInfo{Free: 1, Total: 100}, nil }
	o.RebootRequired = func() (bool, error) { return true, nil }
	var notices []string
	o.Notify = func(n Notice) { notices = append(notices, n.Event) }
	state := filepath.Join(base, "notice-retry")
	m, e := New(c, state, o)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(filepath.Join(state, "stats.json"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = m.Watch(context.Background()); e == nil {
		t.Fatal("persist failure ignored")
	}
	if len(notices) != 0 {
		t.Fatal("notice emitted before persisted dedup state")
	}
	status := m.Status()
	if status.RebootNotified || !status.LowDiskNotice.IsZero() {
		t.Fatalf("failed persist suppressed future notices: %+v", status)
	}
	if e = os.Remove(filepath.Join(state, "stats.json")); e != nil {
		t.Fatal(e)
	}
	if e = m.Watch(context.Background()); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(notices, []string{"disk.low", "reboot.required"}) {
		t.Fatalf("recovered notices=%v", notices)
	}
	if e = m.Watch(context.Background()); e != nil {
		t.Fatal(e)
	}
	if len(notices) != 2 {
		t.Fatal("successful notices repeated")
	}
}

func TestBackupRejectsArchiveDirectoryOverlappingLiveVolume(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprint(nested), func(t *testing.T) {
			_, s, c, o, base := fixture(t)
			c.Backup.Dir = filepath.Join(base, "volumes")
			s.Volume = filepath.Join(c.Backup.Dir, s.ID)
			if nested {
				s.Volume = filepath.Join(s.Volume, "live")
			}
			put(t, s.Volume, "cfg/private.cfg", "untouched")
			m, e := New(c, filepath.Join(base, "overlap-state"), o)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = m.Backup(context.Background(), s); e == nil {
				t.Fatal("backup archive directory overlaps live volume")
			}
			var names []string
			filepath.WalkDir(c.Backup.Dir, func(name string, d os.DirEntry, e error) error {
				if e != nil {
					return e
				}
				if !d.IsDir() {
					names = append(names, name)
				}
				return nil
			})
			if len(names) != 1 || filepath.Base(names[0]) != "private.cfg" {
				t.Fatalf("backup wrote files beneath live-volume archive root: %v", names)
			}
			b, _ := os.ReadFile(filepath.Join(s.Volume, "cfg/private.cfg"))
			if string(b) != "untouched" {
				t.Fatal("live volume modified")
			}
		})
	}
}

func TestMaintenanceCountsRealDockerImagePruneOutput(t *testing.T) {
	_, _, c, o, base := fixture(t)
	c.Maintenance.BuildCache = false
	c.Maintenance.Journal = false
	o.Command = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("Deleted Images:\ndeleted: sha256:111111\ndeleted: sha256:222222\n\nTotal reclaimed space: 1.25MB\n"), nil
	}
	m, e := New(c, filepath.Join(base, "docker-stdout"), o)
	if e != nil {
		t.Fatal(e)
	}
	results, e := m.Maintain(context.Background(), true)
	if e != nil {
		t.Fatal(e)
	}
	if len(results) != 1 || results[0].Items != 2 || results[0].Bytes != 1250000 {
		t.Fatalf("actual Docker stdout miscounted: %+v", results)
	}
	if total := m.Status().MaintenanceTotals["images"]; total.Items != 2 || total.Bytes != 1250000 {
		t.Fatalf("persisted Docker totals=%+v", total)
	}
}
