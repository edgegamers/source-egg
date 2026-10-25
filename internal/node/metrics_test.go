package node

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/edgegamers/source-egg/internal/game"
)

func TestMetricsKeepUnknownPlayerCountsAndUseDockerStats(t *testing.T) {
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			w.Write([]byte(`{"ApiVersion":"1.44"}`))
		case "/v1.44/containers/docker1/stats":
			if r.URL.Query().Get("stream") != "false" {
				t.Error("streaming stats requested")
			}
			w.Write([]byte(`{"cpu_stats":{"cpu_usage":{"total_usage":20},"system_cpu_usage":200,"online_cpus":2},"precpu_stats":{"cpu_usage":{"total_usage":10},"system_cpu_usage":100},"memory_stats":{"usage":1024,"limit":2048,"stats":{"inactive_file":24}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer h.Close()
	cfg := DefaultConfig()
	cfg.StateDir = t.TempDir()
	d, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	d.Docker = &Docker{HTTP: h.Client(), Base: h.URL}
	p, _ := game.Lookup("tf2")
	id, _ := p.Identity("public")
	d.servers["srv"] = Server{UUID: "srv", ID: "docker1", Boot: "boot", Running: true, Identity: id}
	s := d.sampleMetrics(context.Background())
	if len(s.Servers) != 1 || s.Servers[0].Players != -1 || s.Servers[0].Sample.Memory != 1000 || s.Servers[0].Sample.CPU != 20 {
		t.Fatalf("bad sample %+v", s)
	}
	d.states["srv"] = State{Boot: "old", Ready: true, Players: 0, UpdatedAt: time.Now()}
	s = d.sampleMetrics(context.Background())
	if s.Servers[0].Connected {
		t.Fatal("old boot heartbeat reported connected")
	}
}
