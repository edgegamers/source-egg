package egg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPluginNoticesBypassFiltersAndRemainMaskedInLog(t *testing.T) {
	cfg := optionsConfig(t, map[string]string{"ENABLE_FILTER": "1", "CONSOLE_LOG_LEVEL": "ERROR", "SERVER_PASSWORD": "private-secret"})
	writeOptionFile(t, cfg.Dir, ".source/configs/console-filter.json", `{"preview_mode":true,"patterns":["SourceEgg"]}`)
	writeOptionFile(t, cfg.Dir, "srcds_run", "#!/bin/sh\nprintf '%s\\n' 'SourceEgg | Plugins | test package private-secret available'\n")
	if err := os.Chmod(filepath.Join(cfg.Dir, "srcds_run"), 0755); err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(t.TempDir(), "console")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	previous := os.Stdout
	os.Stdout = output
	defer func() { os.Stdout = previous }()
	e := Egg{Config: cfg, Installer: &installRecorder{}}
	if err = e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{output.Name(), filepath.Join(cfg.Dir, ".source/logs/console.log")} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "SourceEgg | Plugins | test package [redacted] available") || strings.Contains(string(data), "private-secret") || strings.Contains(string(data), "Filter preview") {
			t.Fatalf("plugin notice hidden or unmasked in %s: %q", file, data)
		}
	}
}

// A stalled package request must neither block panel input nor prevent shutdown.
func TestConsoleCommandsBoundWorkAndCancelWithoutBlockingPanel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	var once sync.Once
	queue := newConsoleCommands(
		func(line string) bool { return strings.HasPrefix(line, "egg plugins") },
		func(ctx context.Context, line string) { once.Do(func() { close(started) }); <-ctx.Done() },
		func(string) {},
	)
	done := make(chan struct{})
	go func() { queue.run(ctx); close(done) }()
	if queue.intercept("status") || !queue.intercept("egg plugins check") {
		t.Fatal("incorrect command interception")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not receive queued command")
	}
	inputDone := make(chan struct{})
	go func() {
		for range 1000 {
			if !queue.intercept("egg plugins check") {
				t.Error("overflowed reserved command would reach the game")
			}
		}
		close(inputDone)
	}()
	select {
	case <-inputDone:
	case <-time.After(time.Second):
		t.Fatal("full command queue blocked panel input")
	}
	if queue.dropped.Load() == 0 {
		t.Fatal("command queue was not bounded")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not cancel stalled work")
	}
}

func TestRunRoutesPluginCommandsToWorkerAndOrdinaryCommandsToGame(t *testing.T) {
	cfg := optionsConfig(t, map[string]string{"CONSOLE_LOG_LEVEL": "ERROR"})
	writeOptionFile(t, cfg.Dir, "srcds_run", "#!/bin/sh\nwhile read line; do echo \"got:$line\"; [ \"$line\" = quit ] && exit 0; done\n")
	if err := os.Chmod(filepath.Join(cfg.Dir, "srcds_run"), 0755); err != nil {
		t.Fatal(err)
	}
	panel, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer panel.Close()
	defer writer.Close()
	previous := os.Stdin
	os.Stdin = panel
	defer func() { os.Stdin = previous }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	e := Egg{Config: cfg, Installer: &installRecorder{}}
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	if _, err = writer.WriteString("egg plugins help do-not-forward-secret\n"); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(cfg.Dir, ".source/logs/console.log")
	for {
		data, _ := os.ReadFile(logPath)
		if strings.Contains(string(data), "SourceEgg | Plugins | Commands:") {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("game stopped before handling plugin command: %v", err)
		case <-ctx.Done():
			t.Fatal("plugin command did not reach the worker")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err = writer.WriteString("status\nquit\n"); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("ordinary quit did not stop game")
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "do-not-forward-secret") || !strings.Contains(string(data), "got:status") || !strings.Contains(string(data), "got:quit") {
		t.Fatalf("incorrect console routing: %q", data)
	}
}
