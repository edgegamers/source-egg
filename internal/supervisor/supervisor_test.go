package supervisor

import (
	"context"
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
