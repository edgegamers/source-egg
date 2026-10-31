package plugins

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setAutomaticPolicy(t *testing.T, volume string, enabled bool, overrides map[string]bool) {
	t.Helper()
	filename := filepath.Join(volume, manifestPath)
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["auto_approve"] = enabled
	for _, value := range manifest["plugins"].([]any) {
		p := value.(map[string]any)
		if override, ok := overrides[p["id"].(string)]; ok {
			p["auto_approve"] = override
		}
	}
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func automaticInstalled(t *testing.T, m *Manager, id string) Snapshot {
	t.Helper()
	installed, err := m.installer.Installed()
	if err != nil {
		t.Fatal(err)
	}
	return installed[id].Current
}

func TestAutomaticInstallsFirstVersionAndStartupUpdates(t *testing.T) {
	m, source, volume, notices := managerFixture(t)
	setAutomaticPolicy(t, volume, true, nil)
	for _, version := range []string{"v1", "v2"} {
		source.version, source.body = version, version
		if err := m.Startup(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := automaticInstalled(t, m, "demo").Version; got != version {
			t.Fatalf("startup installed %q, want %q; %s", got, version, notices)
		}
		data, err := os.ReadFile(filepath.Join(volume, "tf/addons/sourcemod/plugins/demo.smx"))
		if err != nil || string(data) != version {
			t.Fatalf("installed bytes = %q, %v", data, err)
		}
	}
}

func TestAutomaticRuntimeChecksQueueAndPersistUntilRestart(t *testing.T) {
	m, source, volume, notices := managerFixture(t)
	setAutomaticPolicy(t, volume, true, nil)
	m.Command(context.Background(), "egg plugins check")
	queued, ok := m.state.Pending["demo"]
	if !ok || queued.Kind != "install" {
		t.Fatalf("automatic install not queued: %s", notices)
	}
	if _, err := os.Stat(filepath.Join(volume, "tf/addons/sourcemod/plugins/demo.smx")); !os.IsNotExist(err) {
		t.Fatal("runtime check wrote a live game file")
	}
	source.offline = true
	next, err := New(volume, "tf", source, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Startup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := automaticInstalled(t, next, "demo").ID; got != queued.Snapshot.ID {
		t.Fatalf("persisted install = %q, want %q", got, queued.Snapshot.ID)
	}
}

func TestAutomaticOptInInstallsExistingStagingOfflineWithoutChangingPackage(t *testing.T) {
	for _, global := range []bool{true, false} {
		t.Run(map[bool]string{true: "manifest policy", false: "plugin override"}[global], func(t *testing.T) {
			m, source, volume, notices := managerFixture(t)
			m.Command(context.Background(), "egg plugins check")
			staged := m.state.Available["demo"]
			overrides := map[string]bool{}
			if !global {
				overrides["demo"] = true
			}
			setAutomaticPolicy(t, volume, global, overrides)
			source.offline = true
			if err := m.Startup(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := automaticInstalled(t, m, "demo").ID; got != staged.ID {
				t.Fatalf("existing staging changed or did not install: %q, want %q; %s", got, staged.ID, notices)
			}
		})
	}
}

func TestAutomaticPerPluginOverride(t *testing.T) {
	for _, test := range []struct {
		name             string
		global, override bool
		want             string
	}{
		{"manual plugin in automatic manifest", true, false, ""},
		{"automatic plugin in manual manifest", false, true, "v1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, _, volume, notices := managerFixture(t)
			setAutomaticPolicy(t, volume, test.global, map[string]bool{"demo": test.override})
			if err := m.Startup(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := automaticInstalled(t, m, "demo").Version; got != test.want {
				t.Fatalf("installed = %q, want %q; %s", got, test.want, notices)
			}
			m.Command(context.Background(), "egg plugins list")
			mode := "manual"
			if test.override {
				mode = "auto"
			}
			if !strings.Contains(notices.String(), "approval="+mode) {
				t.Fatalf("list omitted effective approval policy: %s", notices)
			}
		})
	}
}

func TestAutomaticQueuesAllCachedPackagesBeforeProviderConsumesStartupBudget(t *testing.T) {
	m, _, volume, notices := managerFixture(t)
	second := m.manifest.Plugins[0]
	second.ID = "second"
	second.Mappings = []Mapping{{From: "demo.smx", To: "addons/sourcemod/plugins/second.smx"}}
	manifest := m.manifest
	manifest.Plugins = append(manifest.Plugins, second)
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(volume, manifestPath), data, 0600); err != nil {
		t.Fatal(err)
	}
	m.Command(context.Background(), "egg plugins check")
	if len(m.state.Available) != 2 {
		t.Fatalf("two cached packages required: %s", notices)
	}
	setAutomaticPolicy(t, volume, true, nil)
	m.source, m.defaultSource = cancelledSource{}, false
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := m.Startup(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"demo", "second"} {
		if automaticInstalled(t, m, id).ID == "" {
			t.Fatalf("cached %s did not install after timeout: %s", id, notices)
		}
	}
}

func TestAutomaticRejectsCachedPackageAfterContentRecipeEdit(t *testing.T) {
	m, source, volume, notices := managerFixture(t)
	m.Command(context.Background(), "egg plugins check")
	manifest := m.manifest
	manifest.Plugins[0].Preserve = []string{"addons/sourcemod/plugins/demo.smx"}
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(volume, manifestPath), data, 0600); err != nil {
		t.Fatal(err)
	}
	setAutomaticPolicy(t, volume, true, nil)
	source.offline = true
	if err := m.Startup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if automaticInstalled(t, m, "demo").ID != "" {
		t.Fatalf("installed cached content for an earlier recipe: %s", notices)
	}
}

func TestAutomaticOptOutCancelsOnlyAutomaticPendingInstalls(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "explicit approval"}[explicit], func(t *testing.T) {
			m, source, volume, notices := managerFixture(t)
			setAutomaticPolicy(t, volume, true, nil)
			m.Command(context.Background(), "egg plugins check")
			if _, ok := m.state.Pending["demo"]; !ok {
				t.Fatalf("automatic action missing: %s", notices)
			}
			staged := m.state.Available["demo"]
			if explicit {
				m.Command(context.Background(), "egg plugins approve demo "+staged.ID)
			}
			setAutomaticPolicy(t, volume, false, nil)
			source.offline = true
			next, err := New(volume, "tf", source, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := next.Startup(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := automaticInstalled(t, next, "demo").ID
			if (got != "") != explicit {
				t.Fatalf("opt-out installed %q; explicit=%v", got, explicit)
			}
			if !explicit && next.state.Available["demo"].ID != staged.ID {
				t.Fatal("opt-out discarded staging")
			}
		})
	}
}

func TestAutomaticInstallsRespectExplicitPendingActions(t *testing.T) {
	t.Run("exact manual install", func(t *testing.T) {
		m, source, volume, notices := managerFixture(t)
		m.Command(context.Background(), "egg plugins check")
		m.Command(context.Background(), "egg plugins approve demo "+m.state.Available["demo"].ID)
		setAutomaticPolicy(t, volume, true, nil)
		source.version, source.body = "v2", "two"
		if err := m.Startup(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := automaticInstalled(t, m, "demo").Version; got != "v1" {
			t.Fatalf("explicit approval replaced by %q: %s", got, notices)
		}
	})
	for _, action := range []string{"rollback", "uninstall"} {
		t.Run(action, func(t *testing.T) {
			m, source, volume, notices := managerFixture(t)
			setAutomaticPolicy(t, volume, true, nil)
			if err := m.Startup(context.Background()); err != nil {
				t.Fatal(err)
			}
			first := automaticInstalled(t, m, "demo")
			if first.ID == "" {
				t.Fatalf("initial install missing: %s", notices)
			}
			want := ""
			if action == "rollback" {
				source.version, source.body = "v2", "two"
				if err := m.Startup(context.Background()); err != nil {
					t.Fatal(err)
				}
				m.Command(context.Background(), "egg plugins rollback demo "+first.ID)
				want = "v1"
			} else {
				m.Command(context.Background(), "egg plugins uninstall demo confirm")
			}
			if err := m.Startup(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := automaticInstalled(t, m, "demo").Version; got != want {
				t.Fatalf("%s was undone: got %q, want %q; %s", action, got, want, notices)
			}
		})
	}
}

func TestAutomaticInstallBlocksUnownedFilesAndLocalEdits(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unowned file", true: "local edit"}[installed], func(t *testing.T) {
			m, source, volume, notices := managerFixture(t)
			setAutomaticPolicy(t, volume, true, nil)
			if installed {
				if err := m.Startup(context.Background()); err != nil {
					t.Fatal(err)
				}
				if automaticInstalled(t, m, "demo").ID == "" {
					t.Fatalf("initial install missing: %s", notices)
				}
			}
			target := filepath.Join(volume, "tf/addons/sourcemod/plugins/demo.smx")
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("local file"), 0600); err != nil {
				t.Fatal(err)
			}
			source.version, source.body = "v2", "two"
			if err := m.Startup(context.Background()); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "local file" {
				t.Fatalf("local file replaced: %q, %v", data, err)
			}
			if _, ok := m.state.Pending["demo"]; ok || !strings.Contains(notices.String(), "automatic install blocked") {
				t.Fatalf("conflict not reported and blocked: %s", notices)
			}
		})
	}
}

func TestAutomaticCheckReplacesPendingWithLatestOrClearsWhenCurrent(t *testing.T) {
	m, source, volume, notices := managerFixture(t)
	setAutomaticPolicy(t, volume, true, nil)
	if err := m.Startup(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"v2", "v3"} {
		source.version, source.body = version, version
		m.Command(context.Background(), "egg plugins check")
		if got := m.state.Pending["demo"].Snapshot.Version; got != version {
			t.Fatalf("queued %q, want %q; %s", got, version, notices)
		}
	}
	source.version, source.body = "v1", "version1"
	if err := m.Startup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := automaticInstalled(t, m, "demo").Version; got != "v1" {
		t.Fatalf("stale automatic install applied: %q", got)
	}
	if _, ok := m.state.Pending["demo"]; ok {
		t.Fatal("stale automatic install retained")
	}
}

func TestAutomaticSourceBuildRetainsReadyVersionAfterFailure(t *testing.T) {
	m, _, volume, notices := managerFixture(t)
	p := buildRecipe(t)
	p.ID = "demo"
	p.Build.IncludeDirs = nil
	p.Mappings = []Mapping{{From: "compiled/demo.smx", To: "addons/sourcemod/plugins/demo.smx"}, {From: "cfg/demo.cfg", To: "cfg/sourcemod/demo.cfg"}}
	data, _ := json.Marshal(Manifest{Version: 1, Plugins: []Plugin{p}})
	if err := os.WriteFile(filepath.Join(volume, manifestPath), data, 0600); err != nil {
		t.Fatal(err)
	}
	setAutomaticPolicy(t, volume, true, nil)
	archive := func(body string) string {
		return packageTar(t, []packageEntry{{name: "repo/scripting/demo.sp", body: body}, {name: "repo/cfg/demo.cfg", body: "defaults"}})
	}
	source := &sourceManagerFixture{version: strings.Repeat("a", 40), archive: archive("STABLE")}
	m.source, m.defaultSource, m.compiler = source, false, fixtureCompiler(t)
	if err := m.Startup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := automaticInstalled(t, m, "demo").Version; got != source.version {
		t.Fatalf("source did not install: %q; %s", got, notices)
	}
	config := filepath.Join(volume, "tf/cfg/sourcemod/demo.cfg")
	if err := os.WriteFile(config, []byte("local settings"), 0600); err != nil {
		t.Fatal(err)
	}
	source.version = strings.Repeat("b", 40)
	m.Command(context.Background(), "egg plugins check")
	ready := m.state.Available["demo"]
	m.Command(context.Background(), "egg plugins check")
	if source.downloads != 2 || m.state.Pending["demo"].Snapshot.ID != ready.ID {
		t.Fatalf("cached source rebuilt or not queued: %d downloads; %s", source.downloads, notices)
	}
	source.version, source.archive = strings.Repeat("c", 40), archive("FAIL")
	m.Command(context.Background(), "egg plugins check")
	if m.state.Pending["demo"].Snapshot.ID != ready.ID {
		t.Fatalf("failed build replaced ready automatic package: %s", notices)
	}
	source.offline = true
	if err := m.Startup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := automaticInstalled(t, m, "demo").ID; got != ready.ID {
		t.Fatalf("ready source did not install offline: %q; %s", got, notices)
	}
	if data, _ := os.ReadFile(config); string(data) != "local settings" {
		t.Fatal("automatic update overwrote local settings")
	}
}

func TestAutomaticInstallsHonorPinnedRelease(t *testing.T) {
	var origin string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/test/demo/releases/tags/v1":
			json.NewEncoder(w).Encode(map[string]any{"id": 1, "tag_name": "v1", "assets": []map[string]string{{"name": "demo.smx", "browser_download_url": origin + "/v1.smx"}}})
		case "/v1.smx":
			w.Write([]byte("pinned version"))
		default:
			t.Errorf("pinned automatic install requested unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	origin = srv.URL
	m, _, volume, notices := managerFixture(t)
	p := m.manifest.Plugins[0]
	p.Pin, p.Source.URL = "v1", origin
	data, _ := json.Marshal(Manifest{Version: 1, Plugins: []Plugin{p}})
	if err := os.WriteFile(filepath.Join(volume, manifestPath), data, 0600); err != nil {
		t.Fatal(err)
	}
	setAutomaticPolicy(t, volume, true, nil)
	m.source, m.defaultSource = &HTTPSource{Client: srv.Client()}, false
	if err := m.Startup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := automaticInstalled(t, m, "demo").Version; got != "v1" {
		t.Fatalf("pinned version did not install: %q; %s", got, notices)
	}
}
