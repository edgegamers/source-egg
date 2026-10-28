package supervisor

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPTYGracefulStopAndConsoleForwarding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var lines []string
	s := &Server{Cmd: exec.Command("/bin/sh", "-c", `echo READY; while read line; do echo "got:$line"; [ "$line" = quit ] && exit 0; done`), StopCommand: "quit", Grace: time.Second, Line: func(line string) { mu.Lock(); lines = append(lines, line); mu.Unlock() }}
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.Send("status") == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor failed to stop")
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(strings.Join(lines, "|"), "got:quit") {
		t.Fatal(lines)
	}
	if !s.Stopping() {
		t.Fatal("clean stop marked crash")
	}
}

// Forwarding a reserved line (including its first read) would expose it to the
// game's terminal; running the callback under mu would deadlock its Send.
func TestPanelInterceptsSplitLinesWithoutBlockingSendOrQuit(t *testing.T) {
	panel, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer panel.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var mu sync.Mutex
	var lines, inputs []string
	ready := make(chan struct{})
	var s *Server
	s = &Server{
		Cmd:   exec.Command("/bin/sh", "-c", `echo READY; while read line; do echo "got:$line"; [ "$line" = quit ] && exit 0; done`),
		Stdin: panel, StopCommand: "quit", Grace: 100 * time.Millisecond,
		Line: func(line string) {
			mu.Lock()
			lines = append(lines, line)
			mu.Unlock()
			if line == "READY" {
				close(ready)
			}
		},
		OnInput: func(line string) { mu.Lock(); inputs = append(inputs, line); mu.Unlock() },
		InterceptInput: func(line string) bool {
			if strings.HasPrefix(line, "egg plugins") {
				if err := s.Send("injected"); err != nil {
					t.Error(err)
				}
				return true
			}
			return false
		},
	}
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("server did not start")
	}
	for _, part := range []string{"e", "gg plug", "ins check private-token", "\r\nstatus\nquit\n"} {
		if _, err := writer.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	writer.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("interception deadlocked Send or quit")
	}
	mu.Lock()
	defer mu.Unlock()
	output := strings.Join(lines, "\n")
	if strings.Contains(output, "egg plug") || strings.Contains(output, "private-token") {
		t.Fatalf("reserved input reached the PTY: %q", output)
	}
	for _, command := range []string{"got:injected", "got:status", "got:quit"} {
		if !strings.Contains(output, command) {
			t.Errorf("lost %q in %q", command, output)
		}
	}
	if !s.Stopping() {
		t.Fatal("panel quit was not recognized")
	}
	if strings.Join(inputs, ",") != "status,quit" {
		t.Fatalf("forwarded-input observer received reserved command: %q", inputs)
	}
}

func TestPanelInputDiscardsOversizedLinesAndResumesAtNextCommand(t *testing.T) {
	terminal, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	var notices []string
	s := &Server{ptmx: terminal, Line: func(line string) { notices = append(notices, line) }}
	input := panelInput{server: s}
	if err = input.feed([]byte("egg plugins add " + strings.Repeat("sensitive", 40_000))); err != nil {
		t.Fatal(err)
	}
	if err = input.feed([]byte("\r\n  status  \r")); err != nil {
		t.Fatal(err)
	}
	if err = input.feed([]byte("\nquit\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(terminal.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "  status  \nquit\n" {
		t.Fatalf("oversized command leaked or later commands changed: %q", data)
	}
	if len(notices) != 1 || strings.Contains(notices[0], "sensitive") {
		t.Fatalf("expected one safe input-limit notice: %q", notices)
	}
	if !s.Stopping() {
		t.Fatal("lost quit after oversized line")
	}
}
