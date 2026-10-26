package node

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestControlClosesPerRequestConnections(t *testing.T) {
	c := DefaultConfig()
	c.StateDir = t.TempDir()
	l, e := net.Listen("unix", filepath.Join(c.StateDir, "control.sock"))
	if e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }), ConnState: func(c net.Conn, s http.ConnState) {
		mu.Lock()
		defer mu.Unlock()
		if s == http.StateClosed {
			delete(connections, c)
		} else {
			connections[c] = true
		}
	}}
	defer server.Close()
	go server.Serve(l)
	for i := 0; i < 30; i++ {
		if _, e := Control(context.Background(), c, "GET", "/metrics"); e != nil {
			t.Fatal(e)
		}
	}
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		n := len(connections)
		mu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d per-request control connections still open", n)
		}
		time.Sleep(time.Millisecond)
	}
}
