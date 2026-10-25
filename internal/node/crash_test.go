package node

import (
	"context"
	"github.com/edgegamers/source-egg/internal/game"
	"os"
	"path/filepath"
	"testing"
)

func TestCrashReportIsBoundedAndSavedToPrivateNodeState(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StateDir = t.TempDir()
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := game.Lookup("css")
	id, _ := p.Identity("public")
	server := Server{UUID: "server-a", Identity: id}
	if _, err = d.handle(context.Background(), server, Request{Action: "crash", ExitCode: 139, Console: []string{"segmentation fault"}}); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(cfg.StateDir, "crashes/server-a/*.json"))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	info, err := os.Stat(files[0])
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("crash report is not private", err)
	}
	if _, err = d.handle(context.Background(), server, Request{Action: "crash", Console: make([]string, 65)}); err == nil {
		t.Fatal("accepted oversized crash report")
	}
}
