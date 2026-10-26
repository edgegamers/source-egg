package observability

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCrashTotalEvidenceBoundAndCancelledPublication(t *testing.T) {
	c := testConfig(t)
	c.Crashes.MaxBundleBytes = 4 << 20
	now := time.Now().UTC()
	m, e := New(c, Options{Now: func() time.Time { return now }})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	s := server(t)
	// Sparse regular files are small on disk but count at their full tar size.
	for _, name := range []string{"a.dmp", "b.dmp", "c.dmp"} {
		f, e := os.Create(filepath.Join(s.Volume, name))
		if e != nil {
			t.Fatal(e)
		}
		f.Truncate(2 << 20)
		f.Close()
		os.Chtimes(filepath.Join(s.Volume, name), now, now)
	}
	b, e := m.capture(context.Background(), s, CrashReport{Time: now, Lines: []string{"bounded"}})
	if e != nil {
		t.Fatal(e)
	}
	if len(b.Files) != 1 || len(b.Skipped) != 2 || b.Bytes > 4<<20 {
		t.Fatalf("unbounded bundle %+v", b)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.UUID = "cancelled"
	if _, e = m.capture(ctx, s, CrashReport{}); !errors.Is(e, context.Canceled) {
		t.Fatalf("ignored cancellation %v", e)
	}
	entries, e := m.ListCrashes(context.Background(), s.UUID)
	if e != nil || len(entries) != 0 {
		t.Fatalf("cancelled bundle published %+v %v", entries, e)
	}
}

func TestCrashInspectionSanitizesStoredTailAgain(t *testing.T) {
	c := testConfig(t)
	m, e := New(c, Options{})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	s := server(t)
	b, e := m.capture(context.Background(), s, CrashReport{})
	if e != nil {
		t.Fatal(e)
	}
	f, e := os.Create(filepath.Join(c.Crashes.Dir, s.UUID, b.Name))
	if e != nil {
		t.Fatal(e)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	payload := "\x1b]0;bad-title\x07safe\x1b[2J\rpassword=private\n"
	tw.WriteHeader(&tar.Header{Name: "console.txt", Mode: 0600, Size: int64(len(payload))})
	io.WriteString(tw, payload)
	tw.Close()
	gz.Close()
	f.Close()
	tail, e := m.Tail(context.Background(), s.UUID, 1)
	if e != nil || strings.Contains(tail, "bad-title") || strings.Contains(tail, "private") || strings.ContainsAny(tail, "\x1b\x07\r") || !strings.Contains(tail, "safe") {
		t.Fatalf("unsafe inspection %q %v", tail, e)
	}
}

func TestWebhookCheckIsReadOnlyAndDisabledDefaultsHaveNoFilesystemEffects(t *testing.T) {
	c := DefaultConfig()
	m, e := New(c, Options{})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	if _, e = m.CheckWebhook(context.Background()); e == nil {
		t.Fatal("disabled webhook check succeeded")
	}
	if m.Log(Server{UUID: "id"}, "game", "disabled") || m.Crash(Server{UUID: "id"}, CrashReport{}) {
		t.Fatal("disabled modules accepted input")
	}
	c = testConfig(t)
	c.Alerts.Enabled = true
	c.Alerts.Webhook = "https://example.com/secret"
	m, e = New(c, Options{HTTP: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatalf("webhook check posted %s", r.Method)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"name":"Example"}`))}, nil
	})})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	name, e := m.CheckWebhook(context.Background())
	if e != nil || name != "Example" || m.Status().AlertsSent != 0 {
		t.Fatalf("check %q %v %+v", name, e, m.Status())
	}
}

func TestMissingServerInventoryIsEmpty(t *testing.T) {
	c := testConfig(t)
	m, e := New(c, Options{})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	if entries, e := m.ListLogs(context.Background(), "not-yet-running"); e != nil || len(entries) != 0 {
		t.Fatalf("log inventory %+v %v", entries, e)
	}
	if entries, e := m.ListCrashes(context.Background(), "not-yet-running"); e != nil || len(entries) != 0 {
		t.Fatalf("crash inventory %+v %v", entries, e)
	}
}

func TestSourceUpdateFailureAndRestartEventCompatibility(t *testing.T) {
	c := testConfig(t)
	c.Alerts.Enabled = true
	c.Alerts.Webhook = "https://example.com/hook"
	c.Alerts.Events = []string{"game.update.failed", "server.restart"}
	m, e := New(c, Options{})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	for _, kind := range c.Alerts.Events {
		if !m.Notify(Notice{Kind: kind, Server: server(t)}) {
			t.Fatalf("rejected compatible event %s", kind)
		}
	}
}

func TestSecondRateLimitDelaysFollowingBatchWithoutThirdAttempt(t *testing.T) {
	c := testConfig(t)
	c.Alerts.Enabled = true
	c.Alerts.Webhook = "https://example.com/hook"
	ticks := make(chan time.Time, 2)
	delays := make(chan time.Duration, 2)
	calls := 0
	m, e := New(c, Options{After: func(d time.Duration) <-chan time.Time { delays <- d; return ticks }, HTTP: transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(`{"retry_after":2}`))}, nil
	})})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	done := make(chan error, 1)
	go func() { done <- m.post(context.Background(), payload{}) }()
	<-delays
	ticks <- time.Now()
	select {
	case d := <-delays:
		if d < 2*time.Second {
			t.Fatal("short cooldown")
		}
		ticks <- time.Now()
	case e := <-done:
		t.Fatalf("second 429 ignored server cooldown: %v", e)
	}
	if e = <-done; e == nil || calls != 2 {
		t.Fatalf("attempts %d error %v", calls, e)
	}
}
