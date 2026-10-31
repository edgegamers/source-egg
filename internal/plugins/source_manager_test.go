package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type sourceManagerFixture struct {
	version, archive string
	offline          bool
	downloads        int
}

type cancelledSource struct{}

func (cancelledSource) Resolve(ctx context.Context, _ Plugin) (Release, error) {
	<-ctx.Done()
	return Release{}, ctx.Err()
}
func (cancelledSource) Download(context.Context, Plugin, Release, string) error {
	return errors.New("cancelled check must not download")
}

func TestManagerSourceCancelledStartupCheckRetries(t *testing.T) {
	m, _, volume, _ := managerFixture(t)
	p := buildRecipe(t)
	data, _ := json.Marshal(Manifest{Version: 1, Plugins: []Plugin{p}})
	if err := os.WriteFile(filepath.Join(volume, ".source/plugins.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	m.source, m.defaultSource = cancelledSource{}, false
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	m.Command(ctx, "egg plugins check")
	if _, checked := m.state.Checked[p.ID]; checked {
		t.Fatal("startup cancellation delays the source retry for six hours")
	}
}

func (s *sourceManagerFixture) Resolve(context.Context, Plugin) (Release, error) {
	if s.offline {
		return Release{}, errors.New("source unavailable")
	}
	return Release{Version: s.version, ID: s.version, Asset: Asset{Name: "source.tar.gz"}}, nil
}
func (s *sourceManagerFixture) Download(_ context.Context, _ Plugin, _ Release, dest string) error {
	s.downloads++
	data, err := os.ReadFile(s.archive)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0600)
}

func TestManagerSourceBuildLifecycle(t *testing.T) {
	volume := t.TempDir()
	os.MkdirAll(filepath.Join(volume, ".source"), 0700)
	os.MkdirAll(filepath.Join(volume, "cstrike"), 0700)
	p := buildRecipe(t)
	p.Build.IncludeDirs = nil
	p.Mappings = []Mapping{{From: "compiled/demo.smx", To: "addons/sourcemod/plugins/demo.smx"}, {From: "cfg/default.cfg", To: "cfg/sourcemod/demo.cfg"}}
	writeRecipe := func() {
		t.Helper()
		data, _ := json.Marshal(Manifest{Version: 1, Plugins: []Plugin{p}})
		if err := os.WriteFile(filepath.Join(volume, ".source/plugins.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeRecipe()
	archive := func(body string) string {
		return packageTar(t, []packageEntry{{name: "repo/scripting/demo.sp", body: body}, {name: "repo/cfg/default.cfg", body: "original config"}})
	}
	source := &sourceManagerFixture{version: strings.Repeat("a", 40), archive: archive("OK")}
	var notices strings.Builder
	m, err := New(volume, "cstrike", source, func(s string) { notices.WriteString(s + "\n") })
	if err != nil {
		t.Fatal(err)
	}
	m.compiler = fixtureCompiler(t)
	ctx := context.Background()
	m.Command(ctx, "egg plugins check")
	first := m.state.Available[p.ID]
	if first.ID == "" {
		t.Fatal(notices.String())
	}
	if source.downloads != 1 {
		t.Fatalf("downloads: %d", source.downloads)
	}
	m.Command(ctx, "egg plugins check")
	if source.downloads != 1 {
		t.Fatal("unchanged available source was rebuilt")
	}
	// Rebuild deterministic output after cache corruption. Publication must repair
	// the existing same-ID package, rather than discard the fresh correct bytes.
	stableArchive := archive("STABLE")
	source.archive = stableArchive
	source.version = strings.Repeat("d", 40)
	m.Command(ctx, "egg plugins check")
	stable := m.state.Available[p.ID]
	cacheFile := filepath.Join(volume, stable.Directory, "addons/sourcemod/plugins/demo.smx")
	if err := os.WriteFile(cacheFile, []byte("damaged cache"), 0600); err != nil {
		t.Fatal(err)
	}
	m.Command(ctx, "egg plugins check")
	if m.state.Available[p.ID].ID != stable.ID || !m.cachedSourceExists(stable) {
		t.Fatal("rebuilding identical source output did not repair corrupted staging")
	}
	// Continue the approval lifecycle with the original staged commit.
	source.version, source.archive = strings.Repeat("a", 40), archive("OK")
	m.Command(ctx, "egg plugins check")
	first = m.state.Available[p.ID]
	target := filepath.Join(volume, "cstrike/addons/sourcemod/plugins/demo.smx")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("source check installed files")
	}
	m.Command(ctx, "egg plugins approve "+p.ID+" "+first.ID)
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("source approval hot installed")
	}
	// A newer commit must not change the bytes that were already approved.
	source.version = strings.Repeat("b", 40)
	m.Command(ctx, "egg plugins check")
	if err := m.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	installed, err := m.installer.Installed()
	if err != nil || installed[p.ID].Current.ID != first.ID {
		t.Fatalf("wrong source installed: %#v %v", installed, err)
	}
	original, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	second := m.state.Available[p.ID]
	if second.ID == "" || second.ID == first.ID {
		t.Fatal("new source revision missing")
	}
	config := filepath.Join(volume, "cstrike/cfg/sourcemod/demo.cfg")
	if err := os.WriteFile(config, []byte("local settings"), 0600); err != nil {
		t.Fatal(err)
	}
	// A broken new build retains the installed files and the previous staging.
	source.version = strings.Repeat("c", 40)
	source.archive = archive("FAIL")
	m.Command(ctx, "egg plugins check")
	if m.state.Available[p.ID].ID != second.ID {
		t.Fatal("failed build discarded existing staging")
	}
	if data, _ := os.ReadFile(target); string(data) != string(original) {
		t.Fatal("failed build changed live plugin")
	}
	m.Command(ctx, "egg plugins approve "+p.ID+" "+second.ID)
	source.offline = true
	if err := m.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	m.Command(ctx, "egg plugins rollback "+p.ID+" "+first.ID)
	if err := m.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	installed, _ = m.installer.Installed()
	if installed[p.ID].Current.ID != first.ID {
		t.Fatal("source rollback failed: " + notices.String())
	}
	if data, _ := os.ReadFile(config); string(data) != "local settings" {
		t.Fatal("source install/rollback replaced configuration")
	}
	// Source compile settings participate in exact-package approval validation.
	m.Command(ctx, "egg plugins approve "+p.ID+" "+second.ID)
	p.Build.Sources = []string{"scripting/demo.sp"}
	writeRecipe()
	if err := m.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.state.Pending[p.ID]; ok {
		t.Fatal("build recipe edit retained approval")
	}
}
