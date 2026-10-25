package observability

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	c := DefaultConfig()
	c.Logs.Enabled = true
	c.Crashes.Enabled = true
	c.Logs.Dir = filepath.Join(t.TempDir(), "logs")
	c.Crashes.Dir = filepath.Join(t.TempDir(), "crashes")
	return c
}
func start(t *testing.T, m *Manager) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; m.Close() })
}
func await(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}
func server(t *testing.T) Server {
	return Server{UUID: "abcd-1234", Volume: t.TempDir(), Game: "tf2", Name: "Example", Map: "cp_badlands", Players: 3, Versions: map[string]string{"sourcemod": "1.12"}}
}

// A removed bounded send, invalid identity check, or field redaction breaks this test.
func TestBoundedIngestionAndValidation(t *testing.T) {
	c := testConfig(t)
	c.Logs.QueueSize = 2
	m, e := New(c, Options{})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	s := server(t)
	if !m.Log(s, "game", "one") || !m.Log(s, "game", "two") || m.Log(s, "game", "three") {
		t.Fatal("queue must be bounded")
	}
	s.UUID = "../escape"
	if m.Log(s, "game", "bad") || m.Crash(s, CrashReport{}) {
		t.Fatal("accepted path traversal")
	}
	if m.Status().LogsDropped != 1 {
		t.Fatalf("drop count: %+v", m.Status())
	}
	bad := c
	bad.Alerts.Enabled = true
	bad.Alerts.Webhook = "https://user:secret@example.com/hook"
	if bad.Validate() == nil {
		t.Fatal("accepted webhook credentials")
	}
	bad = c
	bad.Logs.QueueSize = -1
	if bad.Validate() == nil {
		t.Fatal("accepted negative queue")
	}
}

func TestDailyLogsUTCCompressionHardRetentionAndRootSafety(t *testing.T) {
	c := testConfig(t)
	c.Logs.KeepDays = 0
	now := time.Date(2026, 10, 25, 1, 0, 0, 0, time.FixedZone("east", 7200))
	m, e := New(c, Options{Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	start(t, m)
	s := server(t)
	if !m.Log(s, "game", "hello\x1b[31m password=secret") {
		t.Fatal("enqueue")
	}
	await(t, func() bool { return m.Status().LogsWritten == 1 })
	p := filepath.Join(c.Logs.Dir, s.UUID)
	data, e := os.ReadFile(filepath.Join(p, "2026-10-24.log"))
	if e != nil || !strings.Contains(string(data), "hello") || strings.Contains(string(data), "secret") || strings.Contains(string(data), "\x1b") {
		t.Fatalf("log %q %v", data, e)
	}
	os.WriteFile(filepath.Join(p, "2024-01-01.log"), []byte("old"), 0600)
	if e = m.Sweep(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(p, "2024-01-01.log")); !os.IsNotExist(e) {
		t.Fatal("hard retention missing")
	}
	// The UTC day is still current, so it must remain writable, not compressed.
	if _, e = os.Stat(filepath.Join(p, "2026-10-24.log")); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(p, "2026-10-23.log"), []byte("yesterday\n"), 0600)
	if e = m.Sweep(context.Background()); e != nil {
		t.Fatal(e)
	}
	f, e := os.Open(filepath.Join(p, "2026-10-23.log.gz"))
	if e != nil {
		t.Fatal(e)
	}
	gz, e := gzip.NewReader(f)
	if e != nil {
		t.Fatal(e)
	}
	b, e := io.ReadAll(gz)
	gz.Close()
	f.Close()
	if e != nil || string(b) != "yesterday\n" {
		t.Fatalf("gzip %q %v", b, e)
	}
	entries, e := m.ListLogs(context.Background(), s.UUID)
	if e != nil || len(entries) != 2 {
		t.Fatalf("list %+v %v", entries, e)
	}
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "2026-10-22.log"), []byte("outside"), 0600)
	if e = os.Symlink(outside, filepath.Join(c.Logs.Dir, "evil")); e != nil {
		t.Fatal(e)
	}
	if _, e = m.ListLogs(context.Background(), "evil"); e == nil {
		t.Fatal("followed storage symlink")
	}
	m.Sweep(context.Background())
	if _, e = os.Stat(filepath.Join(outside, "2026-10-22.log.gz")); !os.IsNotExist(e) {
		t.Fatal("modified outside root")
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }
func TestAlertsAggregateFilterIdentityRedactAndRetry(t *testing.T) {
	c := testConfig(t)
	c.Alerts.Enabled = true
	c.Alerts.Webhook = "https://discord.com/api/webhooks/123/token"
	c.Alerts.Events = []string{"server.start"}
	c.Alerts.Username = "source-node"
	c.Alerts.AvatarURL = "https://example.com/avatar.png"
	c.Alerts.Name = "node-a"
	ticks := make(chan time.Time, 4)
	delays := make(chan time.Duration, 4)
	bodies := make(chan string, 4)
	var mu sync.Mutex
	calls := 0
	httpClient := transportFunc(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		bodies <- string(b)
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		status := 204
		body := ""
		if n == 1 {
			status = 429
			body = `{"retry_after":0.01}`
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	m, e := New(c, Options{HTTP: httpClient, After: func(d time.Duration) <-chan time.Time { delays <- d; return ticks }})
	if e != nil {
		t.Fatal(e)
	}
	start(t, m)
	s := server(t)
	if m.Notify(Notice{Kind: "password.generated", Server: s, Fields: map[string]string{"password": "unacceptable"}}) {
		t.Fatal("ordinary channel accepted password")
	}
	if m.Notify(Notice{Kind: "server.stop", Server: s}) {
		t.Fatal("filter")
	}
	m.Notify(Notice{Kind: "server.start", Server: s, Fields: map[string]string{"password": "secret", "reason": "token=abcdef", "ports": "27015"}})
	m.Notify(Notice{Kind: "server.start", Server: s})
	if d := <-delays; d != 3*time.Second {
		t.Fatalf("aggregation delay %s", d)
	}
	ticks <- time.Now()
	<-bodies
	if d := <-delays; d < 10*time.Millisecond {
		t.Fatalf("rate delay %s", d)
	}
	ticks <- time.Now()
	body := <-bodies
	await(t, func() bool { return m.Status().AlertsSent == 1 })
	var payload struct {
		Username string            `json:"username"`
		Avatar   string            `json:"avatar_url"`
		Embeds   []json.RawMessage `json:"embeds"`
	}
	if e = json.Unmarshal([]byte(body), &payload); e != nil {
		t.Fatal(e)
	}
	if payload.Username != "source-node" || payload.Avatar == "" || len(payload.Embeds) != 2 || !strings.Contains(body, "node-a") || !strings.Contains(body, "abcd-1234") || strings.Contains(body, "secret") || strings.Contains(body, "abcdef") || strings.Contains(body, "unacceptable") {
		t.Fatalf("payload %s", body)
	}
	if m.Status().AlertsRetried != 1 {
		t.Fatalf("status %+v", m.Status())
	}
}

func TestCrashBoundedTailNearbyFilesRateLimitInventory(t *testing.T) {
	c := testConfig(t)
	c.Crashes.MaxBundleBytes = 4 << 20
	c.Crashes.KeepCount = 1
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	m, e := New(c, Options{Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	start(t, m)
	s := server(t)
	for _, p := range []string{"recent.log", "pak.vpk", "Steam/secret.log", "old.log"} {
		p = filepath.Join(s.Volume, p)
		os.MkdirAll(filepath.Dir(p), 0700)
		os.WriteFile(p, []byte("evidence"), 0600)
		os.Chtimes(p, now, now)
	}
	os.Chtimes(filepath.Join(s.Volume, "old.log"), now.Add(-time.Hour), now.Add(-time.Hour))
	big, e := os.Create(filepath.Join(s.Volume, "big.dmp"))
	if e != nil {
		t.Fatal(e)
	}
	big.Truncate((512 << 20) + 1)
	big.Close()
	os.Chtimes(filepath.Join(s.Volume, "big.dmp"), now, now)
	outside := filepath.Join(t.TempDir(), "outside.log")
	os.WriteFile(outside, []byte("secret outside"), 0600)
	os.Symlink(outside, filepath.Join(s.Volume, "link.log"))
	lines := make([]string, 510)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%03d", i)
	}
	lines[509] = "last\x1b[2J password=secret"
	if !m.Crash(s, CrashReport{ExitCode: 139, Time: now, Lines: lines}) || m.Crash(s, CrashReport{Lines: lines}) {
		t.Fatal("rate limit")
	}
	await(t, func() bool { return m.Status().CrashesWritten == 1 })
	list, e := m.ListCrashes(context.Background(), s.UUID)
	if e != nil || len(list) != 1 {
		t.Fatalf("inventory %+v %v", list, e)
	}
	if list[0].ExitCode != 139 || list[0].Map != "cp_badlands" || list[0].Players != 3 {
		t.Fatalf("metadata %+v", list[0])
	}
	tail, e := m.Tail(context.Background(), s.UUID, 1)
	if e != nil || strings.Contains(tail, "line-009") || !strings.Contains(tail, "line-010") || strings.Contains(tail, "secret") || strings.Contains(tail, "\x1b") {
		t.Fatalf("tail %q %v", tail, e)
	}
	f, e := os.Open(filepath.Join(c.Crashes.Dir, s.UUID, list[0].Name))
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	gz, e := gzip.NewReader(f)
	if e != nil {
		t.Fatal(e)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	members := map[string]string{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		b, _ := io.ReadAll(tr)
		members[h.Name] = string(b)
	}
	if members["files/recent.log"] != "evidence" || members["versions.txt"] == "" || members["info.txt"] == "" {
		t.Fatalf("missing evidence %+v", members)
	}
	for _, p := range []string{"files/pak.vpk", "files/Steam/secret.log", "files/old.log", "files/link.log", "files/big.dmp"} {
		if _, ok := members[p]; ok {
			t.Fatalf("unsafe member %s", p)
		}
	}
	if !strings.Contains(members["skipped.txt"], "big.dmp") {
		t.Fatal("large file exclusion not recorded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = m.Tail(ctx, s.UUID, 1); e == nil {
		t.Fatal("ignored cancellation")
	}
}
