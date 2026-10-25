package observability

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestAggregationWaitsFullWindowWhenBatchIsFull(t *testing.T) {
	c := testConfig(t)
	c.Alerts.Enabled = true
	c.Alerts.QueueSize = 2
	c.Alerts.Webhook = "https://example.com/hook"
	tick := make(chan time.Time, 1)
	armed := make(chan struct{}, 1)
	posted := make(chan struct{}, 1)
	m, e := New(c, Options{After: func(time.Duration) <-chan time.Time { armed <- struct{}{}; return tick }, HTTP: transportFunc(func(*http.Request) (*http.Response, error) {
		posted <- struct{}{}
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	})})
	if e != nil {
		t.Fatal(e)
	}
	start(t, m)
	s := server(t)
	m.Notify(Notice{Kind: "server.start", Server: s})
	m.Notify(Notice{Kind: "server.start", Server: s})
	<-armed
	select {
	case <-posted:
		t.Fatal("posted before three second aggregation timer fired")
	case <-time.After(30 * time.Millisecond):
	}
	tick <- time.Now()
	await(t, func() bool { return m.Status().AlertsSent == 1 })
}

func TestStalledWebhookDoesNotBlockLogOrCrashWorkerAndCancels(t *testing.T) {
	c := testConfig(t)
	c.Alerts.Enabled = true
	c.Alerts.QueueSize = 2
	c.Alerts.Webhook = "https://example.com/hook"
	entered := make(chan struct{}, 1)
	m, e := New(c, Options{After: func(time.Duration) <-chan time.Time { ch := make(chan time.Time, 1); ch <- time.Now(); return ch }, HTTP: transportFunc(func(r *http.Request) (*http.Response, error) {
		entered <- struct{}{}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	defer m.Close()
	s := server(t)
	m.Notify(Notice{Kind: "server.start", Server: s})
	<-entered
	for i := 0; i < 100; i++ {
		m.Notify(Notice{Kind: "server.start", Server: s})
	}
	if m.Status().AlertsDropped == 0 {
		t.Fatal("unbounded alerts")
	}
	m.Log(s, "game", "persist independently")
	m.Crash(s, CrashReport{Lines: []string{"crash independently"}})
	await(t, func() bool { return m.Status().LogsWritten == 1 && m.Status().CrashesWritten == 1 })
	cancel()
	select {
	case e = <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked by webhook")
	}
	if m.Notify(Notice{Kind: "server.start", Server: s}) || m.Log(s, "game", "closed") {
		t.Fatal("accepted after close")
	}
}

func TestCrashRateLimitSurvivesManagerRestart(t *testing.T) {
	c := testConfig(t)
	now := time.Now().UTC()
	s := server(t)
	m, e := New(c, Options{Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	start(t, m)
	m.Crash(s, CrashReport{Lines: []string{"crash"}})
	await(t, func() bool { return m.Status().CrashesWritten == 1 })
	again, e := New(c, Options{Now: func() time.Time { return now.Add(20 * time.Second) }})
	if e != nil {
		t.Fatal(e)
	}
	defer again.Close()
	if again.Crash(s, CrashReport{}) {
		t.Fatal("restart bypassed crash-loop rate limit")
	}
	if again.Status().CrashesRateLimited != 1 {
		t.Fatal("missing rate counter")
	}
}

func TestCrashCountAgeRetentionAndUnicodeTailBound(t *testing.T) {
	c := testConfig(t)
	c.Crashes.KeepCount = 1
	var mu sync.Mutex
	now := time.Now().UTC()
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	m, e := New(c, Options{Now: clock})
	if e != nil {
		t.Fatal(e)
	}
	start(t, m)
	s := server(t)
	lines := make([]string, 500)
	for i := range lines {
		lines[i] = strings.Repeat("界", 4000)
	}
	m.Crash(s, CrashReport{Lines: lines})
	await(t, func() bool { return m.Status().CrashesWritten+m.Status().CrashesFailed == 1 })
	if m.Status().CrashesFailed != 0 {
		t.Fatal("unicode lines exceeded byte bound")
	}
	tail, e := m.Tail(context.Background(), s.UUID, 1)
	if e != nil || len(tail) > 500*4001 || !utf8.ValidString(tail) {
		t.Fatalf("invalid tail bytes=%d err=%v", len(tail), e)
	}
	mu.Lock()
	now = now.Add(61 * time.Second)
	mu.Unlock()
	if !m.Crash(s, CrashReport{Lines: []string{"second"}}) {
		t.Fatal("new minute rejected")
	}
	await(t, func() bool { return m.Status().CrashesWritten == 2 })
	list, e := m.ListCrashes(context.Background(), s.UUID)
	if e != nil || len(list) != 1 {
		t.Fatalf("count retention %d %v", len(list), e)
	}
	tail, e = m.Tail(context.Background(), s.UUID, 1)
	if e != nil || tail != "second\n" {
		t.Fatalf("wrong newest %q %v", tail, e)
	}
	mu.Lock()
	now = now.AddDate(0, 0, 31)
	mu.Unlock()
	if e = m.Sweep(context.Background()); e != nil {
		t.Fatal(e)
	}
	list, e = m.ListCrashes(context.Background(), s.UUID)
	if e != nil || len(list) != 0 {
		t.Fatalf("age retention %d %v", len(list), e)
	}
}

func TestPrivateStorageAndVolumeSymlinksNeverEscape(t *testing.T) {
	c := testConfig(t)
	outside := t.TempDir()
	if e := os.Symlink(outside, c.Logs.Dir); e != nil {
		t.Fatal(e)
	}
	if m, e := New(c, Options{}); e == nil {
		m.Close()
		t.Fatal("accepted private directory link")
	}
	os.Remove(c.Logs.Dir)
	m, e := New(c, Options{})
	if e != nil {
		t.Fatal(e)
	}
	start(t, m)
	s := server(t)
	os.Symlink(outside, filepath.Join(c.Logs.Dir, s.UUID))
	m.Log(s, "game", "must not escape")
	await(t, func() bool { return m.Status().LogsFailed == 1 })
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("escaped log root")
	}
	s.Volume = filepath.Join(t.TempDir(), "link")
	os.Symlink(outside, s.Volume)
	m.Crash(s, CrashReport{})
	await(t, func() bool { return m.Status().CrashesFailed == 1 })
	list, e := m.ListCrashes(context.Background(), s.UUID)
	if e != nil || len(list) != 0 {
		t.Fatalf("partial bundle published %+v %v", list, e)
	}
}

func TestDailyLogSizeCapIsObservable(t *testing.T) {
	c := testConfig(t)
	c.Logs.MaxDailyBytes = 4096
	m, e := New(c, Options{})
	if e != nil {
		t.Fatal(e)
	}
	start(t, m)
	s := server(t)
	m.Log(s, "game", strings.Repeat("a", 3900))
	await(t, func() bool { return m.Status().LogsWritten == 1 })
	m.Log(s, "game", strings.Repeat("b", 3900))
	await(t, func() bool { return m.Status().LogsFailed == 1 })
	entries, e := m.ListLogs(context.Background(), s.UUID)
	if e != nil || len(entries) != 1 || entries[0].Bytes > 4096 {
		t.Fatalf("unbounded daily log %+v %v", entries, e)
	}
}

func TestWebhookHonorsLongRetryAfterAndDoesNotLeakErrors(t *testing.T) {
	c := testConfig(t)
	c.Alerts.Enabled = true
	c.Alerts.Webhook = "https://example.com/hook-secret"
	ticks := make(chan time.Time, 4)
	delays := make(chan time.Duration, 4)
	calls := 0
	client := transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"600"}}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
		}
		return nil, errors.New("transport error https://example.com/hook-secret")
	})
	m, e := New(c, Options{HTTP: client, After: func(d time.Duration) <-chan time.Time { delays <- d; return ticks }})
	if e != nil {
		t.Fatal(e)
	}
	start(t, m)
	m.Notify(Notice{Kind: "node.update"})
	<-delays
	ticks <- time.Now()
	if d := <-delays; d < 600*time.Second {
		t.Fatalf("retried before Retry-After: %s", d)
	}
	ticks <- time.Now()
	await(t, func() bool { return m.Status().AlertsFailed == 1 })
	if strings.Contains(m.Status().LastError, "hook-secret") {
		t.Fatal("webhook leaked in status")
	}
}

func TestSweepRecoversInterruptedCompression(t *testing.T) {
	c := testConfig(t)
	m, e := New(c, Options{})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	s := server(t)
	p := filepath.Join(c.Logs.Dir, s.UUID)
	os.MkdirAll(p, 0700)
	date := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	os.WriteFile(filepath.Join(p, date+".log"), []byte("recover me"), 0600)
	os.WriteFile(filepath.Join(p, date+".log.gz.tmp"), []byte("partial"), 0600)
	if e = m.Sweep(context.Background()); e != nil {
		t.Fatalf("stale temporary file blocked sweep: %v", e)
	}
	if _, e = os.Stat(filepath.Join(p, date+".log.gz")); e != nil {
		t.Fatal(e)
	}
}

func TestConfiguredSecretRedactionAndTerminalSequences(t *testing.T) {
	c := testConfig(t)
	m, e := New(c, Options{Redact: func(s string) string { return strings.ReplaceAll(s, "arbitrary-credential", "[PRIVATE]") }})
	if e != nil {
		t.Fatal(e)
	}
	start(t, m)
	s := server(t)
	m.Log(s, "game", "\x1b]0;evil title\x07 arbitrary-credential\x1b[31m\x08done")
	await(t, func() bool { return m.Status().LogsWritten == 1 })
	entries, e := m.ListLogs(context.Background(), s.UUID)
	if e != nil || len(entries) != 1 {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(c.Logs.Dir, s.UUID, entries[0].Name))
	if e != nil || strings.Contains(string(b), "arbitrary-credential") || strings.Contains(string(b), "evil title") || strings.ContainsAny(string(b), "\x1b\x08\x07") || !strings.Contains(string(b), "[PRIVATE]") {
		t.Fatalf("unsafe output %q %v", b, e)
	}
}

func TestProtectedStorageRootsRejectedWithoutMutation(t *testing.T) {
	for _, path := range []string{"/", "/etc", "/tmp/..", "/var", "/var/lib", "/usr", "/home", "/proc", "/sys", "/dev"} {
		t.Run(path, func(t *testing.T) {
			c := testConfig(t)
			c.Logs.Dir = path
			if c.Validate() == nil {
				t.Fatalf("accepted protected storage root %q", path)
			}
			r, e := privateRoot(path)
			if e == nil {
				r.Close()
				t.Fatalf("opened protected storage root %q", path)
			}
		})
	}
}
