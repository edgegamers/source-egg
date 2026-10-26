package node

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/edgegamers/source-egg/internal/game"
	"github.com/edgegamers/source-egg/internal/operations"
)

func TestOperationsControlRechecksStoppedServerBeforeRestore(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StateDir = t.TempDir()
	cfg.Operations.Backup.MinFreeBytes = 0
	volume := t.TempDir()
	os.MkdirAll(filepath.Join(volume, "cstrike/cfg"), 0755)
	target := filepath.Join(volume, "cstrike/cfg/server.cfg")
	os.WriteFile(target, []byte("original"), 0600)
	running := false
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			fmt.Fprint(w, `{"ApiVersion":"1.44"}`)
			return
		}
		fmt.Fprintf(w, `{"Id":"docker1","Name":"/server-abc","Config":{"Image":"ghcr.io/edgegamers/source-egg:stable","Env":["SOURCE_GAME=CS:S"]},"State":{"Running":%t,"StartedAt":"boot"},"Mounts":[{"Destination":"/home/container","Source":%q}]}`, running, volume)
	}))
	defer h.Close()
	d, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	d.Docker = &Docker{HTTP: h.Client(), Base: h.URL}
	p, _ := game.Lookup("css")
	id, _ := p.Identity("public")
	d.servers["server-abc"] = Server{UUID: "server-abc", ID: "docker1", Identity: id, Volume: volume}
	if e = d.control(); e != nil {
		t.Fatal(e)
	}
	defer d.listeners["control"].Close()
	if _, e = Control(context.Background(), cfg, "POST", "/backup?server=server-ab"); e != nil {
		t.Fatal(e)
	}
	body, e := Control(context.Background(), cfg, "GET", "/backups?server=server-abc")
	if e != nil {
		t.Fatal(e)
	}
	var records []operations.BackupRecord
	if json.Unmarshal(body, &records) != nil || len(records) != 1 {
		t.Fatal(string(body))
	}
	os.WriteFile(target, []byte("changed"), 0600)
	running = true
	if _, e = Control(context.Background(), cfg, "POST", "/restore?server=server-abc&index=1"); e == nil {
		t.Fatal("restored running server")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "changed" {
		t.Fatal("running server modified")
	}
	running = false
	if _, e = Control(context.Background(), cfg, "POST", "/restore?server=server-abc&index=1"); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(target)
	if string(b) != "original" {
		t.Fatal("restore did not use selected backup")
	}
}
