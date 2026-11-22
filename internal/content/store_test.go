package content

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/edgegamers/source-egg/internal/game"
)

type fixtureInstaller struct {
	build string
	fail  bool
}

func (f *fixtureInstaller) Install(ctx context.Context, id game.Identity, dir string) error {
	p, _ := game.Lookup(id.Game)
	os.MkdirAll(filepath.Join(dir, p.Dir, "cfg"), 0755)
	os.MkdirAll(filepath.Join(dir, "steamapps"), 0755)
	os.WriteFile(filepath.Join(dir, strings.TrimPrefix(p.Executable, "./")), []byte("#!/bin/sh\nexit 0\n"), 0755)
	os.WriteFile(filepath.Join(dir, p.Dir, "gameinfo.txt"), []byte("game"), 0644)
	os.WriteFile(filepath.Join(dir, p.Dir, "pak01_dir.vpk"), []byte("index-"+f.build), 0644)
	os.WriteFile(filepath.Join(dir, p.Dir, "pak01_000.vpk"), []byte("chunk-"+f.build), 0644)
	os.WriteFile(filepath.Join(dir, p.Dir, "cfg/server.cfg"), []byte("default"), 0644)
	os.WriteFile(filepath.Join(dir, "steamapps/appmanifest_"+id.AppID+".acf"), []byte(`"AppState" { "appid" "`+id.AppID+`" "buildid" "`+f.build+`" "StateFlags" "4" }`), 0644)
	if f.fail {
		return context.Canceled
	}
	return nil
}
func TestImmutableGenerationsAndPrivateEventMaps(t *testing.T) {
	root := t.TempDir()
	p, _ := game.Lookup("css")
	id, _ := p.Identity("public")
	install := &fixtureInstaller{build: "100"}
	s := Store{Dir: filepath.Join(root, "store"), Installer: install}
	a, e := s.Ensure(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	vol := filepath.Join(root, "volume")
	os.MkdirAll(filepath.Join(vol, "cstrike/maps"), 0755)
	os.MkdirAll(filepath.Join(vol, "cstrike/cfg"), 0755)
	os.WriteFile(filepath.Join(vol, "cstrike/maps/event.bsp"), []byte("event"), 0644)
	os.WriteFile(filepath.Join(vol, "cstrike/cfg/server.cfg"), []byte("private"), 0644)
	// Test mount root is the real store root; production uses /opt/source-shared.
	if e = Deploy(a, vol, s.Dir); e != nil {
		t.Fatal(e)
	}
	link, e := os.Readlink(filepath.Join(vol, "cstrike/pak01_dir.vpk"))
	if e != nil || !strings.HasPrefix(link, a.Path) {
		t.Fatal(link, e)
	}
	install.build = "200"
	b, e := s.Ensure(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	old, _ := os.ReadFile(filepath.Join(vol, "cstrike/pak01_000.vpk"))
	if string(old) != "chunk-100" {
		t.Fatal("running generation changed")
	}
	if e = Deploy(b, vol, s.Dir); e != nil {
		t.Fatal(e)
	}
	for path, want := range map[string]string{"cstrike/cfg/server.cfg": "private", "cstrike/maps/event.bsp": "event", "cstrike/pak01_000.vpk": "chunk-200"} {
		data, _ := os.ReadFile(filepath.Join(vol, path))
		if string(data) != want {
			t.Fatalf("%s = %s", path, data)
		}
	}
	if e = s.Prune(map[string]bool{a.Key: true, b.Key: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(a.Path); e != nil {
		t.Fatal("deleted referenced generation")
	}
}
func TestFailedInstallDoesNotReplacePublishedGeneration(t *testing.T) {
	p, _ := game.Lookup("tf2")
	id, _ := p.Identity("public")
	f := &fixtureInstaller{build: "10"}
	s := Store{Dir: t.TempDir(), Installer: f}
	a, e := s.Ensure(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	f.fail = true
	f.build = "11"
	if _, e = s.Ensure(context.Background(), id); e == nil {
		t.Fatal("published failed install")
	}
	current, e := s.Current(id)
	if e != nil || current.Key != a.Key {
		t.Fatal("lost working generation", e)
	}
}
func TestSteamCommandUsesSelectedAppAndBranch(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "steamcmd")
	out := filepath.Join(dir, "args")
	os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+out+"'\nexit 1\n"), 0755)
	p, _ := game.Lookup("dods")
	id, _ := p.Identity("prerelease")
	s := Steam{Executable: script}
	if e := s.Install(context.Background(), id, filepath.Join(dir, "install")); e == nil {
		t.Fatal("ignored failed steamcmd")
	}
	b, _ := os.ReadFile(out)
	if !strings.Contains(string(b), "+app_update\n232290\n-beta\nprerelease\n") {
		t.Fatal(string(b))
	}
}
func TestDeploymentRefusesEscapingSymlink(t *testing.T) {
	p, _ := game.Lookup("css")
	id, _ := p.Identity("public")
	s := Store{Dir: t.TempDir(), Installer: &fixtureInstaller{build: "1"}}
	a, _ := s.Ensure(context.Background(), id)
	vol := t.TempDir()
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(vol, "cstrike"))
	if e := Deploy(a, vol, s.Dir); e == nil {
		t.Fatal("followed planted symlink")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("wrote outside volume")
	}
}

type linkedInstaller struct{ fixtureInstaller }

func (f *linkedInstaller) Install(ctx context.Context, id game.Identity, dir string) error {
	if err := f.fixtureInstaller.Install(ctx, id, dir); err != nil {
		return err
	}
	return os.Symlink("pak01_000.vpk", filepath.Join(dir, "cstrike/alias.vpk"))
}
func TestSnapshotMaterializesConfinedSteamSymlinks(t *testing.T) {
	p, _ := game.Lookup("css")
	id, _ := p.Identity("public")
	s := Store{Dir: t.TempDir(), Installer: &linkedInstaller{fixtureInstaller{build: "100"}}}
	g, err := s.Ensure(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(g.Path, "cstrike/alias.vpk"))
	if err != nil || string(b) != "chunk-100" {
		t.Fatal(string(b), err)
	}
	info, _ := os.Lstat(filepath.Join(g.Path, "cstrike/alias.vpk"))
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("snapshot retains symlink")
	}
}
