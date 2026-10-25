package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDockerStatsUseDeltasAndExcludeInactiveCache(t *testing.T) {
	old := DockerStats{}
	old.CPU.Usage.Total = 10
	old.CPU.System = 100
	old.Networks = map[string]Network{"eth0": {RX: 100, TX: 500}}
	now := old
	now.CPU.Usage.Total = 30
	now.CPU.System = 200
	now.CPU.Online = 4
	now.Memory.Usage = 1000
	now.Memory.Limit = 2000
	now.Memory.Stats = map[string]uint64{"inactive_file": 400}
	now.Networks = map[string]Network{"eth0": {RX: 300, TX: 900}}
	got := Measure(now, &old, time.Second)
	if got.CPU != 80 || got.Memory != 600 || got.RXRate != 200 || got.TXRate != 400 {
		t.Fatalf("wrong measurement: %+v", got)
	}
	now.CPU.System = 50
	now.Networks["eth0"] = Network{RX: 1, TX: 1}
	got = Measure(now, &old, time.Second)
	if got.CPU != 0 || got.RXRate != 0 || got.TXRate != 0 {
		t.Fatalf("counter reset overflowed: %+v", got)
	}
	now.Memory.Stats["inactive_file"] = 5000
	if Measure(now, nil, 0).Memory != 1000 {
		t.Fatal("cache subtraction underflowed")
	}
}

func TestPrometheusEscapesLabelsAndTracksGameState(t *testing.T) {
	snapshot := Snapshot{Version: "test", Channel: "beta", Servers: []Server{{ID: "srv", Game: "css", Name: "evil\x1b[2J", Map: "map\"\nname", Running: true, Connected: true, Players: 4, Sample: Sample{CPU: 12, Memory: 300}}}}
	r := httptest.NewRecorder()
	Handler(func() Snapshot { return snapshot }).ServeHTTP(r, httptest.NewRequest("GET", "/metrics", nil))
	body := r.Body.String()
	if r.Code != 200 || !strings.Contains(body, `source_server_map_info{server="srv",game="css",map="map\"\nname"} 1`) || !strings.Contains(body, "source_server_cpu_percent") {
		t.Fatalf("bad export: %s", body)
	}
	r = httptest.NewRecorder()
	Handler(func() Snapshot { return snapshot }).ServeHTTP(r, httptest.NewRequest("GET", "/other", nil))
	if r.Code != 404 {
		t.Fatal("metrics listener served unknown route")
	}
}

func TestTopSortAndTerminalSanitization(t *testing.T) {
	s := Snapshot{Servers: []Server{{ID: "b", Name: "\x1b[2Jbad", Game: "tf2", Players: 1, Sample: Sample{CPU: 1}}, {ID: "a", Name: "good", Game: "css", Players: 5, Sample: Sample{CPU: 9}}}}
	lines := Render(s, "cpu", 0, 120, 20)
	if strings.Contains(lines, "\x1b") || strings.Index(lines, "good") > strings.Index(lines, "bad") {
		t.Fatalf("unsafe or unsorted terminal output: %q", lines)
	}
}

func TestMetricsConfigRestrictsListenerAndIntervals(t *testing.T) {
	c := DefaultConfig()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Listen = "0.0.0.0:9151"
	if err := c.Validate(); err != nil {
		t.Fatal("explicit public listen should be possible", err)
	}
	c.Listen = "garbage"
	if c.Validate() == nil {
		t.Fatal("bad listen accepted")
	}
}
