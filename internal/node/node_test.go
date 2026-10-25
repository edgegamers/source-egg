package node

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

	"github.com/edgegamers/source-egg/internal/content"
	"github.com/edgegamers/source-egg/internal/fsx"
	"github.com/edgegamers/source-egg/internal/game"
)

func TestDiscoveryRequiresExactApprovedImageAndGame(t *testing.T) {
	raw := `{"Id":"container1","Name":"/server-a","Config":{"Image":"ghcr.io/edgegamers/source-egg:stable","Env":["SOURCE_GAME=CS:S","STEAM_BRANCH=public"]},"State":{"Running":true,"Pid":123,"StartedAt":"boot1"},"Mounts":[{"Destination":"/home/container","Source":"/srv/volumes/a"}]}`
	var c dockerInspect
	json.Unmarshal([]byte(raw), &c)
	s, e := serverFrom(c, []string{"ghcr.io/edgegamers/source-egg"})
	if e != nil {
		t.Fatal(e)
	}
	if s.Identity.Game != "css" || s.UUID != "server-a" {
		t.Fatal(s)
	}
	c.Config.Image = "attacker/ghcr.io/edgegamers/source-egg:stable"
	if _, e = serverFrom(c, []string{"ghcr.io/edgegamers/source-egg"}); e == nil {
		t.Fatal("substring image accepted")
	}
	c.Config.Image = "ghcr.io/edgegamers/source-egg:stable"
	c.Config.Env = []string{"SOURCE_GAME=bad"}
	if _, e = serverFrom(c, []string{"ghcr.io/edgegamers/source-egg"}); e == nil {
		t.Fatal("unknown game accepted")
	}
}

func TestDiscoveryRejectsChangedInstalledProfile(t *testing.T) {
	p, _ := game.Lookup("css")
	id, _ := p.Identity("public")
	dir := t.TempDir()
	if err := game.CheckIdentity(dir, id); err != nil {
		t.Fatal(err)
	}
	var c dockerInspect
	c.ID = "container"
	c.Name = "/server-a"
	c.Config.Image = "ghcr.io/edgegamers/source-egg:stable"
	c.Config.Env = []string{"SOURCE_GAME=TF2"}
	c.Mounts = append(c.Mounts, struct{ Destination, Source string }{"/home/container", dir})
	if _, err := serverFrom(c, []string{"ghcr.io/edgegamers/source-egg"}); err == nil {
		t.Fatal("accepted a changed game while old process could be running")
	}
}

func TestWindowRequiresWindowEvenWhenEmpty(t *testing.T) {
	p := RestartPolicy{Mode: "window", Window: "03:00-04:00"}
	noon := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	if Due(p, noon, noon, 0) {
		t.Fatal("window policy restarted at noon")
	}
	if !Due(p, noon.Add(-12*time.Hour), noon.Add(-9*time.Hour), 5) {
		t.Fatal("did not restart inside window")
	}
}

type noInstall struct{}

func (noInstall) Install(context.Context, game.Identity, string) error { return nil }
func published(t *testing.T, dir, build string) content.Generation {
	t.Helper()
	p, _ := game.Lookup("css")
	id, _ := p.Identity("public")
	key := "generations/" + id.Key() + "/" + build
	path := filepath.Join(dir, key)
	os.MkdirAll(filepath.Join(path, "cstrike"), 0755)
	os.MkdirAll(filepath.Join(path, "steamapps"), 0755)
	os.WriteFile(filepath.Join(path, "srcds_run"), []byte("run"), 0755)
	os.WriteFile(filepath.Join(path, "cstrike/gameinfo.txt"), []byte("game"), 0644)
	os.WriteFile(filepath.Join(path, "steamapps/appmanifest_232330.acf"), []byte(`"appid" "232330" "buildid" "`+build+`" "StateFlags" "4"`), 0644)
	g := content.Generation{Identity: id, Key: key, BuildID: build, Path: path}
	root, _ := os.OpenRoot(dir)
	defer root.Close()
	if err := fsx.JSON(root, "current/"+id.Key()+".json", g, 0644); err != nil {
		t.Fatal(err)
	}
	return g
}
func TestDaemonRestartReconcilesOldServersAndPreservesDeadline(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StateDir = t.TempDir()
	cfg.SharedDir = t.TempDir()
	g := published(t, cfg.SharedDir, "200")
	d, _ := New(cfg)
	since := time.Now().Add(-2 * time.Hour).UTC()
	d.refs["server-a"] = Reference{Current: "generations/css/public/linux-auto/100"}
	d.pending["server-a"] = pending{Generation: g.Key, Since: since}
	if err := d.saveRefs(); err != nil {
		t.Fatal(err)
	}
	again, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	again.Store.Installer = noInstall{}
	again.servers["server-a"] = Server{UUID: "server-a", Running: true, Identity: g.Identity}
	again.checkUpdates(context.Background())
	pending, ok := again.pending["server-a"]
	if !ok || pending.Generation != g.Key || !pending.Since.Equal(since) {
		t.Fatal("lost rollout/deadline on restart", pending)
	}
}
func TestPruneWaitsForBootGenerationReservation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StateDir = t.TempDir()
	cfg.SharedDir = t.TempDir()
	a := published(t, cfg.SharedDir, "100")
	published(t, cfg.SharedDir, "200")
	d, _ := New(cfg)
	if err := d.control(); err != nil {
		t.Fatal(err)
	}
	defer d.listeners["control"].Close()
	d.prepare.Lock()
	done := make(chan error, 1)
	go func() { _, err := Control(context.Background(), cfg, "POST", "/prune"); done <- err }()
	select {
	case err := <-done:
		d.prepare.Unlock()
		t.Fatalf("prune ran during pending boot transaction: %v", err)
	case <-time.After(60 * time.Millisecond):
	}
	d.mu.Lock()
	d.refs["server-a"] = Reference{Pending: a.Key}
	d.mu.Unlock()
	d.prepare.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.Path); err != nil {
		t.Fatal("deleted boot-selected generation", err)
	}
}

func TestGenerationPinIsScopedPersistentAndRejectsWrongGame(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StateDir = t.TempDir()
	cfg.SharedDir = t.TempDir()
	a := published(t, cfg.SharedDir, "100")
	b := published(t, cfg.SharedDir, "200")
	d, _ := New(cfg)
	s := Server{UUID: "server-a", Identity: a.Identity, Running: true}
	d.servers[s.UUID] = s
	if err := d.pinServer(s.UUID, a.Key); err != nil {
		t.Fatal(err)
	}
	selected, err := d.desired(s)
	if err != nil || selected.Key != a.Key {
		t.Fatal("pin ignored", selected, err)
	}
	other := s
	other.UUID = "server-b"
	selected, err = d.desired(other)
	if err != nil || selected.Key != b.Key {
		t.Fatal("pin leaked to other server")
	}
	again, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	selected, err = again.desired(s)
	if err != nil || selected.Key != a.Key {
		t.Fatal("lost persistent pin")
	}
	if err = d.pinServer(s.UUID, "generations/tf2/public/linux64/100"); err == nil {
		t.Fatal("accepted wrong-game pin")
	}
}
func TestRolloutTargetsOnlyRunningMatchingIdentity(t *testing.T) {
	css, _ := game.Lookup("css")
	ci, _ := css.Identity("public")
	tf, _ := game.Lookup("tf2")
	ti, _ := tf.Identity("public")
	now := time.Now()
	servers := []Server{{UUID: "css", Identity: ci, Running: true}, {UUID: "tf", Identity: ti, Running: true}, {UUID: "stopped", Identity: ci, Running: false}}
	targets := Targets(servers, ci)
	if len(targets) != 1 || targets[0].UUID != "css" {
		t.Fatal(targets)
	}
	policy := RestartPolicy{Mode: "empty", MaxDelayMinutes: 60}
	if Due(policy, now, now, -1) {
		t.Fatal("unknown player count treated as empty")
	}
	if !Due(policy, now, now, 0) {
		t.Fatal("empty not restarted")
	}
	if !Due(policy, now.Add(-2*time.Hour), now, 5) {
		t.Fatal("max delay ignored")
	}
}
func TestPasswordRequestCannotSelectAnotherScopeOrGame(t *testing.T) {
	p, _ := game.Lookup("css")
	id, _ := p.Identity("public")
	cfg := DefaultConfig()
	cfg.StateDir = t.TempDir()
	cfg.PasswordScope = "game"
	d, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	s := Server{UUID: "css-a", Identity: id, Running: true, Env: map[string]string{"GENERATE_PASSWORD": "1"}}
	request := httptest.NewRequest("POST", "/", strings.NewReader(`{"version":1,"action":"password","server":"tf-b","game":"tf2","scope":"node"}`))
	w := httptest.NewRecorder()
	d.Handler(s).ServeHTTP(w, request)
	if w.Code < 400 {
		t.Fatal("accepted caller-controlled password identity", w.Body.String())
	}
}
func TestWingsPowerUsesUUIDAndChecksResponse(t *testing.T) {
	seen := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing auth")
		}
		w.WriteHeader(500)
	}))
	defer srv.Close()
	w := Wings{URL: srv.URL, Token: "secret"}
	if e := w.Restart(context.Background(), "server-a"); e == nil {
		t.Fatal("accepted Wings error")
	}
	if seen != "/api/servers/server-a/power" {
		t.Fatal(seen)
	}
}
