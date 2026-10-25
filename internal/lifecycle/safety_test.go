package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func verifiedClient(body string) *http.Client {
	sum := sha256.Sum256([]byte(body))
	return &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "checksums.txt") {
			return response(hex.EncodeToString(sum[:]) + " *sourcenode_linux_amd64\n"), nil
		}
		return response(body), nil
	})}
}
func validCandidate() *Candidate {
	return &Candidate{Version: "v1.2.3", Binary: asset("v1.2.3", "sourcenode_linux_amd64"), Checksums: asset("v1.2.3", "checksums.txt")}
}

// The daemon commonly has a private umask; release executability must not depend on it.
func TestApplyPreservesExecutableModeUnderPrivateUmask(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sourcenode")
	if e := os.WriteFile(path, []byte("old"), 0755); e != nil {
		t.Fatal(e)
	}
	old := syscall.Umask(0077)
	defer syscall.Umask(old)
	m := fixtureManager(t, Config{}, Options{BinaryPath: path, Client: verifiedClient("new"), Run: func(context.Context, string, ...string) error { return nil }})
	if e := m.Apply(context.Background(), validCandidate()); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{path, path + ".prev"} {
		info, e := os.Stat(p)
		if e != nil || info.Mode().Perm() != 0755 {
			t.Fatalf("executable mode %s: %v %v", p, info, e)
		}
	}
}

func TestApplyRejectsSymlinkAndDirectoryPaths(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	path := filepath.Join(outside, "binary")
	os.WriteFile(path, []byte("preserve"), 0755)
	link := filepath.Join(dir, "link")
	os.Symlink(outside, link)
	fileLink := filepath.Join(dir, "file-link")
	os.Symlink(path, fileLink)
	for _, p := range []string{"", "relative", dir, fileLink, filepath.Join(link, "binary"), filepath.Join(dir, "unused", "..")} {
		m := fixtureManager(t, Config{}, Options{BinaryPath: p, Client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("unsafe binary path reached download")
			return nil, nil
		})}})
		if e := m.Apply(context.Background(), validCandidate()); e == nil {
			t.Fatal("accepted unsafe target", p)
		}
	}
	b, _ := os.ReadFile(path)
	if string(b) != "preserve" {
		t.Fatal("symlink target changed")
	}
}

type interruptedBody struct{ sent bool }

func (b *interruptedBody) Read(p []byte) (int, error) {
	if b.sent {
		return 0, io.ErrUnexpectedEOF
	}
	b.sent = true
	return copy(p, "partial"), nil
}
func (*interruptedBody) Close() error { return nil }
func TestInterruptedBinaryRetainsCurrentAndPrevious(t *testing.T) {
	for _, kind := range []string{"read error", "cancelled", "http failure", "empty"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "sourcenode")
			os.WriteFile(bin, []byte("current"), 0755)
			os.WriteFile(bin+".prev", []byte("previous"), 0755)
			base := verifiedClient("complete")
			client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "checksums.txt") {
					return base.Transport.RoundTrip(r)
				}
				switch kind {
				case "read error":
					return &http.Response{StatusCode: 200, Body: &interruptedBody{}}, nil
				case "cancelled":
					return nil, context.Canceled
				case "http failure":
					r := response("unavailable")
					r.StatusCode = 503
					return r, nil
				default:
					return response(""), nil
				}
			})}
			m := fixtureManager(t, Config{}, Options{BinaryPath: bin, Client: client, Run: func(context.Context, string, ...string) error { t.Fatal("failed download reached systemd"); return nil }})
			if e := m.Apply(context.Background(), validCandidate()); e == nil {
				t.Fatal("failed download accepted")
			}
			for p, want := range map[string]string{bin: "current", bin + ".prev": "previous"} {
				b, _ := os.ReadFile(p)
				if string(b) != want {
					t.Fatal("failed download changed", p)
				}
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 2 {
				t.Fatal("partial staging files retained", entries)
			}
		})
	}
}

// A replacement path injected during download must never expose a config file
// as the executable or world-readable .prev backup.
func TestApplyRejectsBinarySwappedToSymlinkDuringDownload(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "sourcenode")
	config := filepath.Join(dir, "config.json")
	os.WriteFile(bin, []byte("old"), 0755)
	os.WriteFile(config, []byte("private"), 0600)
	client := verifiedClient("new")
	base := client.Transport
	client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/sourcenode_linux_amd64") {
			if e := os.Remove(bin); e != nil {
				t.Fatal(e)
			}
			if e := os.Symlink(filepath.Base(config), bin); e != nil {
				t.Fatal(e)
			}
		}
		return base.RoundTrip(r)
	})
	m := fixtureManager(t, Config{}, Options{BinaryPath: bin, Client: client, Run: func(context.Context, string, ...string) error { t.Fatal("changed binary reached restart"); return nil }})
	if e := m.Apply(context.Background(), validCandidate()); e == nil {
		t.Fatal("binary symlink swap accepted")
	}
	if _, e := os.Stat(bin + ".prev"); !os.IsNotExist(e) {
		t.Fatal("private config copied to previous executable")
	}
	info, _ := os.Stat(config)
	b, _ := os.ReadFile(config)
	if string(b) != "private" || info.Mode().Perm() != 0600 {
		t.Fatal("config modified")
	}
}

func TestApplyPinsParentBeforeDownloading(t *testing.T) {
	dir := t.TempDir()
	managed := filepath.Join(dir, "managed")
	outside := filepath.Join(dir, "outside")
	os.Mkdir(managed, 0755)
	os.Mkdir(outside, 0755)
	bin := filepath.Join(managed, "sourcenode")
	os.WriteFile(bin, []byte("old"), 0755)
	os.WriteFile(filepath.Join(outside, "sourcenode"), []byte("untouched"), 0755)
	client := verifiedClient("new")
	base := client.Transport
	client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "checksums.txt") {
			if e := os.Rename(managed, managed+"-old"); e != nil {
				t.Fatal(e)
			}
			if e := os.Symlink(outside, managed); e != nil {
				t.Fatal(e)
			}
		}
		return base.RoundTrip(r)
	})
	m := fixtureManager(t, Config{}, Options{BinaryPath: bin, Client: client, Run: func(context.Context, string, ...string) error { return nil }})
	if e := m.Apply(context.Background(), validCandidate()); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(outside, "sourcenode"))
	if e != nil || string(b) != "untouched" {
		t.Fatal("upgrade crossed swapped parent", e)
	}
	b, e = os.ReadFile(filepath.Join(managed+"-old", "sourcenode"))
	if e != nil || string(b) != "new" {
		t.Fatal("pinned target not upgraded", e)
	}
}

func TestBeforeRestartCanAbortAndRollBack(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "sourcenode")
	os.WriteFile(bin, []byte("old"), 0755)
	callback := false
	m := fixtureManager(t, Config{}, Options{BinaryPath: bin, Client: verifiedClient("new"), BeforeRestart: func(_ context.Context, version string) error {
		callback = true
		b, e := os.ReadFile(bin)
		if e != nil || string(b) != "new" || version != "v1.2.3" {
			t.Fatal("callback occurred before verified swap", version, e)
		}
		return errors.New("cannot persist restart notice")
	}, Run: func(context.Context, string, ...string) error {
		t.Fatal("failed callback reached service request")
		return nil
	}})
	if e := m.Apply(context.Background(), validCandidate()); e == nil || !strings.Contains(e.Error(), "cannot persist restart notice") {
		t.Fatal("callback failure hidden", e)
	}
	if !callback {
		t.Fatal("callback omitted")
	}
	b, _ := os.ReadFile(bin)
	if string(b) != "old" {
		t.Fatal("callback failure did not restore previous binary")
	}
}

func TestBeforeRestartCompletesBeforeServiceRequest(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "sourcenode")
	os.WriteFile(bin, []byte("old"), 0755)
	prepared := false
	m := fixtureManager(t, Config{}, Options{BinaryPath: bin, Client: verifiedClient("new"), BeforeRestart: func(context.Context, string) error { prepared = true; return nil }, Run: func(_ context.Context, name string, args ...string) error {
		if !prepared || name != "systemctl" || strings.Join(args, " ") != "--no-block try-restart sourcenode" {
			t.Fatal("restart ordering or blocking command", prepared, name, args)
		}
		return nil
	}})
	if e := m.Apply(context.Background(), validCandidate()); e != nil {
		t.Fatal(e)
	}
}

func TestReleaseRedirectPolicy(t *testing.T) {
	m := fixtureManager(t, Config{}, Options{})
	for _, raw := range []string{"http://github.com/foo", "https://github.com.evil.test/foo", "https://user:secret@github.com/foo", "https://release-assets.githubusercontent.com:444/file", "https://evil.test/file"} {
		req, _ := http.NewRequest("GET", raw, nil)
		if e := m.options.Client.CheckRedirect(req, nil); e == nil {
			t.Fatal("accepted redirect", raw)
		}
	}
	req, _ := http.NewRequest("GET", "https://release-assets.githubusercontent.com/path", nil)
	if e := m.options.Client.CheckRedirect(req, nil); e != nil {
		t.Fatal(e)
	}
}
func TestConfigValidationAndDevChannel(t *testing.T) {
	for _, c := range []Config{{Channel: "bogus"}, {CheckMinutes: -1}, {Release: "v1.2.3/../../x"}, {Release: "v1.02.3"}, {Release: "v1.2.3-rc..1"}} {
		if _, e := New(c, Options{}); e == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
	m := fixtureManager(t, Config{Channel: "dev", Enabled: true}, Options{Client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("dev made network request"); return nil, nil })}})
	if _, e := m.Check(context.Background(), "v1.0.0"); !errors.Is(e, ErrUpToDate) {
		t.Fatal(e)
	}
}

func TestRunDoesNotRepeatSuccessfulUpdate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	waiting := make(chan struct{}, 1)
	runs := 0
	bin := filepath.Join(t.TempDir(), "sourcenode")
	os.WriteFile(bin, []byte("old"), 0755)
	client := verifiedClient("new")
	base := client.Transport
	client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/releases/download/") {
			return base.RoundTrip(r)
		}
		return response(`[{"tag_name":"v1.2.3","assets":[{"name":"sourcenode_linux_amd64","browser_download_url":"https://github.com/edgegamers/source-egg/releases/download/v1.2.3/sourcenode_linux_amd64"},{"name":"checksums.txt","browser_download_url":"https://github.com/edgegamers/source-egg/releases/download/v1.2.3/checksums.txt"}]}]`), nil
	})
	reported := make(chan Result, 1)
	m := fixtureManager(t, Config{Enabled: true}, Options{BinaryPath: bin, Client: client, Run: func(context.Context, string, ...string) error { runs++; return nil }, After: func(time.Duration) <-chan time.Time { waiting <- struct{}{}; return ticks }})
	done := make(chan struct{})
	go func() { m.Run(ctx, "v1.0.0", func(r Result) { reported <- r }); close(done) }()
	<-waiting
	ticks <- time.Now()
	if r := <-reported; !r.Updated {
		t.Fatal(r)
	}
	<-waiting
	ticks <- time.Now()
	<-waiting
	cancel()
	<-done
	if runs != 1 {
		t.Fatal("successful release reapplied", runs)
	}
	select {
	case r := <-reported:
		t.Fatal("up-to-date check generated result", r)
	default:
	}
}
