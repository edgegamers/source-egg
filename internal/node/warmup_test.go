package node

import (
	"context"
	"github.com/edgegamers/source-egg/internal/game"
	"os"
	"path/filepath"
	"testing"
)

type warmupInstaller struct{}

func (warmupInstaller) Install(_ context.Context, id game.Identity, dir string) error {
	p, _ := game.Lookup(id.Game)
	os.MkdirAll(filepath.Join(dir, p.Dir), 0755)
	os.MkdirAll(filepath.Join(dir, "steamapps"), 0755)
	os.WriteFile(filepath.Join(dir, "srcds_run"), []byte("run"), 0755)
	os.WriteFile(filepath.Join(dir, p.Dir, "gameinfo.txt"), []byte("game"), 0644)
	return os.WriteFile(filepath.Join(dir, "steamapps/appmanifest_"+id.AppID+".acf"), []byte(`"appid" "`+id.AppID+`" "buildid" "100" "StateFlags" "4"`), 0644)
}
func TestWarmupPreparesSelectedGamesWithoutServers(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StateDir = t.TempDir()
	cfg.SharedDir = t.TempDir()
	cfg.PrefetchGames = []string{"css"}
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	d.Store.Installer = warmupInstaller{}
	d.warmup(context.Background())
	p, _ := game.Lookup("css")
	id, _ := p.Identity("public")
	if g, err := d.Store.Current(id); err != nil || g.BuildID != "100" {
		t.Fatal(g, err)
	}
	p, _ = game.Lookup("tf2")
	id, _ = p.Identity("public")
	if _, err = d.Store.Current(id); !os.IsNotExist(err) {
		t.Fatal("downloaded an unselected game", err)
	}
}
func TestConfigRejectsUnknownPrefetchGame(t *testing.T) {
	cfg := DefaultConfig()
	cfg.PrefetchGames = []string{"source2"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("unknown preload game accepted")
	}
}
