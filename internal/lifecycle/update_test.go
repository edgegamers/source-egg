package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
func asset(tag, name string) Asset {
	return Asset{Name: name, URL: "https://github.com/edgegamers/source-egg/releases/download/" + tag + "/" + name}
}
func fixtureRelease(tag string, pre, draft bool, legacy bool) map[string]any {
	sum := "checksums.txt"
	if legacy {
		sum = "sourcenode_linux_amd64.sha256"
	}
	return map[string]any{"tag_name": tag, "prerelease": pre, "draft": draft, "assets": []map[string]string{{"name": "sourcenode_linux_amd64", "browser_download_url": asset(tag, "sourcenode_linux_amd64").URL}, {"name": sum, "browser_download_url": asset(tag, sum).URL}}}
}
func fixtureManager(t *testing.T, c Config, o Options) *Manager {
	t.Helper()
	m, e := New(c, o)
	if e != nil {
		t.Fatal(e)
	}
	return m
}

// Skipping drafts or beta releases incorrectly changes the selected release.
func TestReleaseSelection(t *testing.T) {
	releases := []map[string]any{fixtureRelease("v9.0.0", false, true, false), fixtureRelease("v1.3.0-rc.2", true, false, false), fixtureRelease("v1.2.3", false, false, true), fixtureRelease("v1.3.0-rc.10", true, false, false)}
	for _, tc := range []struct{ channel, exact, want, path string }{{"stable", "", "v1.2.3", "/releases?per_page=100"}, {"beta", "", "v1.3.0-rc.10", "/releases?per_page=100"}, {"stable", "v1.1.0", "v1.1.0", "/releases/tags/v1.1.0"}} {
		t.Run(tc.channel+tc.exact, func(t *testing.T) {
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if !strings.HasSuffix(r.URL.String(), tc.path) {
					t.Fatalf("unexpected lookup %s", r.URL)
				}
				var b []byte
				if tc.exact != "" {
					b, _ = json.Marshal(fixtureRelease(tc.exact, false, false, false))
				} else {
					b, _ = json.Marshal(releases)
				}
				return response(string(b)), nil
			})}
			m := fixtureManager(t, Config{Channel: tc.channel, Release: tc.exact}, Options{Client: client})
			c, e := m.Check(context.Background(), "v1.1.9")
			if e != nil || c.Version != tc.want {
				t.Fatalf("candidate=%+v err=%v", c, e)
			}
			if tc.channel == "stable" && tc.exact == "" && c.Checksums.Name != "sourcenode_linux_amd64.sha256" {
				t.Fatal("legacy checksum not selected")
			}
		})
	}
}
func TestCheckUpToDateAndMissingAssets(t *testing.T) {
	for _, tc := range []struct {
		name, current string
		rel           map[string]any
		wantError     bool
	}{{"equal", "v1.2.3", fixtureRelease("v1.2.3", false, false, false), false}, {"older", "v2.0.0", fixtureRelease("v1.2.3", false, false, false), false}, {"missing checksum", "v1.0.0", map[string]any{"tag_name": "v1.2.3", "assets": []any{}}, true}} {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal([]any{tc.rel})
			m := fixtureManager(t, Config{Channel: "stable"}, Options{Client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return response(string(b)), nil })}})
			_, e := m.Check(context.Background(), tc.current)
			if tc.wantError {
				if e == nil {
					t.Fatal("missing release assets accepted")
				}
			} else if !errors.Is(e, ErrUpToDate) {
				t.Fatalf("got %v", e)
			}
		})
	}
}

// No unverified data may replace the running binary, and a failed restart rolls back.
func TestApplyVerificationAndRollback(t *testing.T) {
	for _, tc := range []struct {
		name, sums  string
		commandFail bool
		want        string
		wantErr     bool
	}{{"verified", "valid", false, "new binary", false}, {"legacy", "legacy", false, "new binary", false}, {"mismatch", "bad", false, "old binary", true}, {"missing", "missing", false, "old binary", true}, {"duplicate", "duplicate", false, "old binary", true}, {"restart failure", "valid", true, "old binary", true}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "sourcenode")
			os.WriteFile(bin, []byte("old binary"), 0755)
			cfg := filepath.Join(dir, "config.json")
			os.WriteFile(cfg, []byte("private config"), 0600)
			sum := sha256.Sum256([]byte("new binary"))
			line := hex.EncodeToString(sum[:]) + "  sourcenode_linux_amd64\n"
			sums := line
			switch tc.sums {
			case "bad":
				sums = strings.Repeat("0", 64) + "  sourcenode_linux_amd64\n"
			case "missing":
				sums = strings.Repeat("0", 64) + "  something-else\n"
			case "duplicate":
				sums = line + line
			}
			name := "checksums.txt"
			if tc.sums == "legacy" {
				name = "sourcenode_linux_amd64.sha256"
			}
			c := &Candidate{Version: "v1.2.3", Binary: asset("v1.2.3", "sourcenode_linux_amd64"), Checksums: asset("v1.2.3", name)}
			runs := 0
			m := fixtureManager(t, Config{Channel: "stable"}, Options{BinaryPath: bin, Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/"+name) {
					return response(sums), nil
				}
				return response("new binary"), nil
			})}, Run: func(_ context.Context, name string, args ...string) error {
				runs++
				if name != "systemctl" || strings.Join(args, " ") != "--no-block try-restart sourcenode" {
					t.Fatalf("unsafe command %s %v", name, args)
				}
				data, _ := os.ReadFile(bin)
				if string(data) != "new binary" {
					t.Fatal("command saw incomplete replacement")
				}
				if tc.commandFail {
					return errors.New("injected restart failure")
				}
				return nil
			}})
			e := m.Apply(context.Background(), c)
			if (e != nil) != tc.wantErr {
				t.Fatalf("error=%v", e)
			}
			data, _ := os.ReadFile(bin)
			if string(data) != tc.want {
				t.Fatalf("binary=%s", data)
			}
			data, _ = os.ReadFile(cfg)
			if string(data) != "private config" {
				t.Fatal("configuration changed")
			}
			info, _ := os.Stat(bin)
			if info.Mode().Perm() != 0755 {
				t.Fatal("binary not executable")
			}
			if (tc.sums == "valid" || tc.sums == "legacy") && runs != 1 {
				t.Fatal("service not restarted")
			}
			if runs > 0 {
				prev, _ := os.ReadFile(bin + ".prev")
				if string(prev) != "old binary" {
					t.Fatal("previous binary not retained")
				}
			}
		})
	}
}
func TestApplyRejectsUntrustedCandidate(t *testing.T) {
	for _, raw := range []string{"http://github.com/edgegamers/source-egg/releases/download/v1.2.3/sourcenode_linux_amd64", "https://evil.test/binary", "https://github.com/other/repo/releases/download/v1.2.3/sourcenode_linux_amd64"} {
		m := fixtureManager(t, Config{Channel: "stable"}, Options{BinaryPath: filepath.Join(t.TempDir(), "sourcenode")})
		c := &Candidate{Version: "v1.2.3", Binary: Asset{Name: "sourcenode_linux_amd64", URL: raw}, Checksums: asset("v1.2.3", "checksums.txt")}
		if e := m.Apply(context.Background(), c); e == nil {
			t.Fatal("accepted untrusted candidate", raw)
		}
	}
}

// Disabled scheduling does no network work; enabled scheduling waits and honors cancellation.
func TestScheduledUpdate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	waiting := make(chan time.Duration, 1)
	reported := make(chan Result, 1)
	dir := t.TempDir()
	bin := filepath.Join(dir, "sourcenode")
	os.WriteFile(bin, []byte("old"), 0755)
	sum := sha256.Sum256([]byte("new"))
	b, _ := json.Marshal([]any{fixtureRelease("v1.2.3", false, false, false)})
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/releases/download/") {
			if strings.HasSuffix(r.URL.Path, "checksums.txt") {
				return response(hex.EncodeToString(sum[:]) + "  sourcenode_linux_amd64"), nil
			}
			return response("new"), nil
		}
		return response(string(b)), nil
	})}
	checkedAt := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	m := fixtureManager(t, Config{Enabled: true, Channel: "stable", CheckMinutes: 5}, Options{BinaryPath: bin, Client: client, After: func(d time.Duration) <-chan time.Time { waiting <- d; return ticks }, Run: func(context.Context, string, ...string) error { return nil }})
	m.options.Now = func() time.Time { return checkedAt }
	done := make(chan struct{})
	go func() { m.Run(ctx, "v1.0.0", func(r Result) { reported <- r }); close(done) }()
	if d := <-waiting; d != 5*time.Minute {
		t.Fatalf("interval=%s", d)
	}
	ticks <- time.Now()
	r := <-reported
	if !r.Updated || r.Version != "v1.2.3" || r.Err != nil || !r.CheckedAt.Equal(checkedAt) {
		t.Fatalf("result=%+v", r)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
	disabled := fixtureManager(t, Config{Channel: "stable"}, Options{After: func(time.Duration) <-chan time.Time { t.Fatal("disabled scheduler ran"); return nil }})
	disabled.Run(context.Background(), "v1", nil)
}
