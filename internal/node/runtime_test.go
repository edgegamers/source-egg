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
		// The control listener starts before discovery and optional workers.
		// Wait for the metrics worker's first sample so this test actually
		// exercises worker shutdown rather than racing initial discovery.
		if _, e := Control(context.Background(), c, "GET", "/modules"); e == nil && !d.metricsSnapshot().At.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("control or optional workers did not start")
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

// Stop requests can arrive as soon as the control socket is available, while
// initial Docker discovery is still in flight. They must release resources and
// finish normally, just like cancellation after the main loop has started.
func TestDaemonStopDuringInitialDiscovery(t *testing.T) {
	for _, blockedPath := range []string{"/version", "/v1.44/containers/json"} {
		t.Run(blockedPath, func(t *testing.T) {
			entered := make(chan struct{})
			h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == blockedPath {
					close(entered)
					<-r.Context().Done()
					return
				}
				if r.URL.Path == "/version" {
					fmt.Fprint(w, `{"ApiVersion":"1.44"}`)
					return
				}
				http.NotFound(w, r)
			}))
			defer h.Close()
			c := DefaultConfig()
			c.StateDir = t.TempDir()
			c.SharedDir = t.TempDir()
			c.CacheDir = t.TempDir()
			c.Shared = false
			c.FrameworkCache = false
			d, err := New(c)
			if err != nil {
				t.Fatal(err)
			}
			d.Docker = &Docker{HTTP: h.Client(), Base: h.URL}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- d.Run(ctx) }()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("initial discovery did not start")
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("normal shutdown returned startup failure: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("initial discovery ignored stop")
			}
			if _, err := Control(context.Background(), c, "GET", "/modules"); err == nil {
				t.Fatal("control socket still serving after stop")
			}
			lock, err := d.lock()
			if err != nil {
				t.Fatal("daemon lock not released", err)
			}
			lock.Close()
		})
	}
}

func TestDaemonInitialDiscoveryFailureIsReported(t *testing.T) {
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer h.Close()
	c := DefaultConfig()
	c.StateDir = t.TempDir()
	c.SharedDir = t.TempDir()
	c.CacheDir = t.TempDir()
	c.Shared = false
	c.FrameworkCache = false
	d, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	d.Docker = &Docker{HTTP: h.Client(), Base: h.URL}
	if err = d.Run(context.Background()); err == nil {
		t.Fatal("Docker startup failure was suppressed")
	}
	if _, err = Control(context.Background(), c, "GET", "/modules"); err == nil {
		t.Fatal("control socket still serving after startup failure")
	}
	lock, err := d.lock()
	if err != nil {
		t.Fatal("daemon lock not released after failure", err)
	}
	lock.Close()
}
