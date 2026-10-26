package node

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDaemonOptionalWorkersStopAndReleaseControlSocket(t *testing.T) {
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			fmt.Fprint(w, `{"ApiVersion":"1.44"}`)
		case "/v1.44/containers/json":
			fmt.Fprint(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer h.Close()
	c := DefaultConfig()
	c.StateDir = t.TempDir()
	c.SharedDir = t.TempDir()
	c.CacheDir = t.TempDir()
	c.Shared = false
	c.FrameworkCache = false
	c.Metrics.Enabled = true
	c.Metrics.Listen = "127.0.0.1:0"
	d, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	d.Docker = &Docker{HTTP: h.Client(), Base: h.URL}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, e := Control(context.Background(), c, "GET", "/modules"); e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("control did not start")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("optional module shutdown blocked daemon")
	}
	if _, e := Control(context.Background(), c, "GET", "/modules"); e == nil {
		t.Fatal("control socket still serving after stop")
	}
	lock, e := d.lock()
	if e != nil {
		t.Fatal("daemon lock not released", e)
	}
	lock.Close()
}
