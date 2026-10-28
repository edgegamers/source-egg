package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixtureSource struct {
	version, body string
	offline       bool
}

func (f *fixtureSource) Resolve(context.Context, Plugin) (Release, error) {
	if f.offline {
		return Release{}, errors.New("offline")
	}
	return Release{Version: f.version, ID: f.version, Asset: Asset{Name: "demo.smx", URL: "https://example.test/demo.smx"}}, nil
}
func (f *fixtureSource) Download(_ context.Context, _ Plugin, _ Release, dest string) error {
	return os.WriteFile(dest, []byte(f.body), 0600)
}
func managerFixture(t *testing.T) (*Manager, *fixtureSource, string, *strings.Builder) {
	t.Helper()
	v := t.TempDir()
	os.MkdirAll(filepath.Join(v, "tf"), 0700)
	os.MkdirAll(filepath.Join(v, ".source"), 0700)
	p := Plugin{ID: "demo", Source: Source{Provider: "github", Repository: "test/demo"}, Asset: "demo.smx", Format: "raw", Mappings: []Mapping{{From: "demo.smx", To: "addons/sourcemod/plugins/demo.smx"}}}
	b, _ := json.Marshal(Manifest{Version: 1, Plugins: []Plugin{p}})
	os.WriteFile(filepath.Join(v, ".source/plugins.json"), b, 0600)
	f := &fixtureSource{version: "v1", body: "version1"}
	out := &strings.Builder{}
	m, e := New(v, "tf", f, func(s string) { out.WriteString(s + "\n") })
	if e != nil {
		t.Fatal(e)
	}
	return m, f, v, out
}
func TestManagerRequiresExactApprovalAndNextStartup(t *testing.T) {
	m, _, v, out := managerFixture(t)
	ctx := context.Background()
	if e := m.Startup(ctx); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(v, "tf/addons/sourcemod/plugins/demo.smx")
	if _, e := os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("check installed without approval")
	}
	m.Command(ctx, "egg plugins approve demo wrong")
	if !strings.Contains(out.String(), "exact") {
		t.Fatal(out.String())
	}
	m.Command(ctx, "egg plugins preview demo")
	id := m.state.Available["demo"].ID
	m.Command(ctx, "egg plugins approve demo "+id)
	if _, e := os.Stat(target); !os.IsNotExist(e) {
		t.Fatal("approval hot installed")
	}
	if e := m.Startup(ctx); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(target)
	if e != nil || string(b) != "version1" {
		t.Fatalf("%q %v notices=%s", b, e, out)
	}
}
func TestManagerApprovalSurvivesNewDiscoveryButNotRecipeEdits(t *testing.T) {
	m, f, v, _ := managerFixture(t)
	ctx := context.Background()
	m.Startup(ctx)
	id := m.state.Available["demo"].ID
	m.Command(ctx, "egg plugins approve demo "+id)
	f.version = "v2"
	f.body = "version2"
	m.Command(ctx, "egg plugins check")
	f.offline = true
	if e := m.Startup(ctx); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(v, "tf/addons/sourcemod/plugins/demo.smx"))
	if string(b) != "version1" {
		t.Fatalf("approval changed to new discovery: %q", b)
	}
	f.offline = false
	m.Command(ctx, "egg plugins check")
	id = m.state.Available["demo"].ID
	m.Command(ctx, "egg plugins approve demo "+id)
	m.Command(ctx, "egg plugins keep demo addons/sourcemod/plugins/demo.smx")
	if e := m.Startup(ctx); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(filepath.Join(v, "tf/addons/sourcemod/plugins/demo.smx"))
	if string(b) != "version1" {
		t.Fatal("recipe edit did not invalidate approval")
	}
}
func TestManagerRemovedRecipeDoesNotUninstall(t *testing.T) {
	m, _, v, _ := managerFixture(t)
	ctx := context.Background()
	m.Startup(ctx)
	m.Command(ctx, "egg plugins approve demo "+m.state.Available["demo"].ID)
	m.Startup(ctx)
	os.WriteFile(filepath.Join(v, ".source/plugins.json"), []byte(`{"version":1,"plugins":[]}`), 0600)
	if e := m.Startup(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(v, "tf/addons/sourcemod/plugins/demo.smx")); e != nil {
		t.Fatal("removing recipe removed files")
	}
	m.Command(ctx, "egg plugins uninstall demo confirm")
	m.Startup(ctx)
	if _, e := os.Stat(filepath.Join(v, "tf/addons/sourcemod/plugins/demo.smx")); !os.IsNotExist(e) {
		t.Fatal("explicit uninstall failed")
	}
}
func TestManagerReloadRejectsMalformedManifestWithoutLosingInstalled(t *testing.T) {
	m, _, v, out := managerFixture(t)
	ctx := context.Background()
	m.Startup(ctx)
	m.Command(ctx, "egg plugins approve demo "+m.state.Available["demo"].ID)
	m.Startup(ctx)
	os.WriteFile(filepath.Join(v, ".source/plugins.json"), []byte(`{"version":1,"plguins":[]}`), 0600)
	if e := m.Startup(ctx); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), "unknown field") {
		t.Fatal(out.String())
	}
	if _, e := os.Stat(filepath.Join(v, "tf/addons/sourcemod/plugins/demo.smx")); e != nil {
		t.Fatal(e)
	}
}

func TestManagerRollbackAndUninstallPreserveOwnerKeep(t *testing.T) {
	m, f, v, out := managerFixture(t)
	ctx := context.Background()
	target := filepath.Join(v, "tf/addons/sourcemod/plugins/demo.smx")
	install := func(version, body string) {
		t.Helper()
		f.version = version
		f.body = body
		m.Command(ctx, "egg plugins check")
		m.Command(ctx, "egg plugins approve demo "+m.state.Available["demo"].ID)
		if e := m.Startup(ctx); e != nil {
			t.Fatal(e)
		}
	}
	install("v1", "one")
	first := m.state.Available["demo"].ID
	installed, e := m.installer.Installed()
	if e != nil {
		t.Fatal(e)
	}
	first = installed["demo"].Current.ID
	install("v2", "two")
	install("v3", "three")
	install("v4", "four")
	installed, e = m.installer.Installed()
	if e != nil {
		t.Fatal(e)
	}
	if len(installed["demo"].Previous) != 2 {
		t.Fatalf("history %v", installed)
	}
	for _, s := range installed["demo"].Previous {
		if s.ID == first {
			t.Fatal("retained more than two previous versions")
		}
	}
	if _, err := os.Stat(filepath.Join(v, ".source/plugins/packages", first)); !os.IsNotExist(err) {
		t.Fatal("unused package was not pruned")
	}
	previous := installed["demo"].Previous[0]
	if e = os.WriteFile(target, []byte("custom"), 0600); e != nil {
		t.Fatal(e)
	}
	m.Command(ctx, "egg plugins rollback demo "+previous.ID)
	if _, ok := m.state.Pending["demo"]; ok {
		t.Fatal("modified file rollback approved without decision")
	}
	m.Command(ctx, "egg plugins keep demo addons/sourcemod/plugins/demo.smx")
	m.Command(ctx, "egg plugins rollback demo "+previous.ID)
	if _, ok := m.state.Pending["demo"]; !ok {
		t.Fatal(out.String())
	}
	m.Startup(ctx)
	b, _ := os.ReadFile(target)
	if string(b) != "custom" {
		t.Fatalf("keep lost at rollback: %q", b)
	}
	m.Command(ctx, "egg plugins uninstall demo confirm")
	m.Startup(ctx)
	b, _ = os.ReadFile(target)
	if string(b) != "custom" {
		t.Fatalf("keep lost at uninstall: %q", b)
	}
}
func TestManagerAddRejectsTrailingJSONAndInvalidStorage(t *testing.T) {
	m, _, v, out := managerFixture(t)
	ctx := context.Background()
	m.Command(ctx, `egg plugins add {"id":"new","source":{"provider":"github","repository":"test/new"},"asset":"demo.smx","mappings":[{"from":"demo.smx","to":"addons/new.smx"}]} garbage`)
	if !strings.Contains(out.String(), "one plugin object") {
		t.Fatal(out.String())
	}
	if len(m.manifest.Plugins) != 1 {
		t.Fatal("malformed add changed manifest")
	}
	os.Remove(filepath.Join(v, ".source/plugins.json"))
	os.Symlink(filepath.Join(v, "outside.json"), filepath.Join(v, ".source/plugins.json"))
	if _, e := New(v, "tf", &fixtureSource{}, nil); e == nil {
		t.Fatal("symlink manifest accepted")
	}
}

func TestManagerCancelClearsPersistedApprovalWithoutChangingFiles(t *testing.T) {
	m, f, v, out := managerFixture(t)
	ctx := context.Background()
	m.Startup(ctx)
	m.Command(ctx, "egg plugins approve demo "+m.state.Available["demo"].ID)
	m.Command(ctx, "egg plugins cancel demo")
	next, e := New(v, "tf", f, func(s string) { out.WriteString(s) })
	if e != nil {
		t.Fatal(e)
	}
	if e = next.Startup(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(v, "tf/addons/sourcemod/plugins/demo.smx")); !os.IsNotExist(e) {
		t.Fatalf("cancelled approval installed: %v %s", e, out)
	}
}

func TestManagerOfflineWarnsAboutKnownStagedUpdate(t *testing.T) {
	m, f, _, out := managerFixture(t)
	ctx := context.Background()
	m.Startup(ctx)
	m.Command(ctx, "egg plugins approve demo "+m.state.Available["demo"].ID)
	m.Startup(ctx)
	f.version = "v2"
	f.body = "two"
	m.Command(ctx, "egg plugins check")
	f.offline = true
	out.Reset()
	if e := m.Startup(ctx); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), "v2") || !strings.Contains(out.String(), "previously staged") {
		t.Fatalf("lost known update notice while offline: %s", out)
	}
}

func TestManagerUninstallKeepsPreviewPolicyAfterUntracking(t *testing.T) {
	for _, localEdit := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit-keep", true: "modified-at-confirmation"}[localEdit], func(t *testing.T) {
			m, f, v, out := managerFixture(t)
			ctx := context.Background()
			m.Startup(ctx)
			m.Command(ctx, "egg plugins approve demo "+m.state.Available["demo"].ID)
			m.Startup(ctx)
			target := filepath.Join(v, "tf/addons/sourcemod/plugins/demo.smx")
			if localEdit {
				if e := os.WriteFile(target, []byte("custom"), 0600); e != nil {
					t.Fatal(e)
				}
			} else {
				m.Command(ctx, "egg plugins keep demo addons/sourcemod/plugins/demo.smx")
			}
			m.Command(ctx, "egg plugins uninstall demo confirm")
			if _, ok := m.state.Pending["demo"]; !ok {
				t.Fatal(out.String())
			}
			// Even reverting a local edit must not expand the set approved for removal.
			if e := os.WriteFile(target, []byte("version1"), 0600); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(v, manifestPath), []byte(`{"version":1,"plugins":[]}`), 0600); e != nil {
				t.Fatal(e)
			}
			restarted, e := New(v, "tf", f, nil)
			if e != nil {
				t.Fatal(e)
			}
			if e = restarted.Startup(ctx); e != nil {
				t.Fatal(e)
			}
			if _, e = os.Stat(target); e != nil {
				t.Fatalf("uninstall deleted file preview promised to preserve: %v; %s", e, out)
			}
		})
	}
}
