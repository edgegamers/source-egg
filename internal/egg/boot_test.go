package egg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/edgegamers/source-egg/internal/game"
	"github.com/edgegamers/source-egg/internal/node"
)

type installRecorder struct{ calls int }

func (i *installRecorder) Install(ctx context.Context, id game.Identity, dir string) error {
	i.calls++
	return nil
}
func TestStandaloneBootAndManagedFallbackBoundary(t *testing.T) {
	cfg := Config{Dir: t.TempDir(), Game: "css", Branch: "public", Mode: "standalone", Port: 27015}
	installer := &installRecorder{}
	e := Egg{Config: cfg, Installer: installer}
	prepared, err := e.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if installer.calls != 1 || prepared.Managed {
		t.Fatal("standalone path not used")
	}
	os.MkdirAll(filepath.Join(cfg.Dir, ".source"), 0755)
	os.WriteFile(filepath.Join(cfg.Dir, ".source/managed.json"), []byte(`{}`), 0600)
	if _, err = e.Prepare(context.Background()); err == nil {
		t.Fatal("fell back to Steam through managed files")
	}
	if installer.calls != 1 {
		t.Fatal("ran Steam on managed volume")
	}
}
func TestGeneratedPasswordRequiresNodeInsteadOfOpeningServer(t *testing.T) {
	cfg := Config{Dir: t.TempDir(), Game: "css", Mode: "standalone", Port: 27015, GeneratePassword: true}
	e := Egg{Config: cfg, Installer: &installRecorder{}}
	if _, err := e.Prepare(context.Background()); err == nil {
		t.Fatal("started without assigned password")
	}
}
func TestNodePasswordAndFrameworkSettingsRemainPrivate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(node.Response{Error: "unavailable"})
	}))
	defer srv.Close()
	cfg := Config{Dir: t.TempDir(), Game: "css", Mode: "managed", Port: 27015}
	installer := &installRecorder{}
	e := Egg{Config: cfg, Installer: installer, Node: srv.Client(), NodeEndpoint: srv.URL}
	if _, err := e.Prepare(context.Background()); err == nil {
		t.Fatal("ignored node failure")
	}
	if installer.calls != 0 {
		t.Fatal("managed mode called local Steam")
	}
}
func TestConfigAndLogMasking(t *testing.T) {
	env := map[string]string{"SOURCE_GAME": "TF2", "SERVER_PORT": "27015", "PASSWORD_SCOPE": "game", "GENERATE_PASSWORD": "1"}
	cfg, err := FromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Game != "tf2" || !cfg.GeneratePassword || !cfg.InstallSourceMod {
		t.Fatal(cfg)
	}
	text := Mask("rcon_password secret +sv_password abc", []string{"secret", "abc"})
	if strings.Contains(text, "secret") || strings.Contains(text, "abc") {
		t.Fatal("credential leaked")
	}
	if strings.Contains(Mask(`"sv_password" = "live-override"`, nil), "live-override") {
		t.Fatal("live console credential leaked")
	}
	env["SERVER_PORT"] = "oops"
	if _, err = FromEnv(func(k string) string { return env[k] }); err == nil {
		t.Fatal("invalid port accepted")
	}
}
