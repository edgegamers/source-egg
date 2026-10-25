package node

import (
	"context"
	"github.com/edgegamers/source-egg/internal/game"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Observation.Run(ctx); close(done) }()
	defer func() { cancel(); <-done; d.Observation.Close() }()
	server := Server{UUID: "server-a", Identity: id, Volume: t.TempDir()}
	if _, err = d.handle(context.Background(), server, Request{Action: "crash", ExitCode: 139, Console: []string{"segmentation fault"}}); err != nil {
		t.Fatal(err)
	}
	var files []string
	deadline := time.Now().Add(time.Second)
	for {
		files, err = filepath.Glob(filepath.Join(cfg.StateDir, "crashes/server-a/*.tar.gz"))
		if len(files) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(files, err)
		}
		time.Sleep(time.Millisecond)
	}
	info, err := os.Stat(files[0])
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("crash report is not private", err)
	}
	if _, err = d.handle(context.Background(), server, Request{Action: "crash", Console: make([]string, 501)}); err == nil {
		t.Fatal("accepted oversized crash report")
	}
}
