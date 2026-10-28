package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func acquisitionPlugin() Plugin {
	return Plugin{ID: "example", Source: Source{Provider: "github", Repository: "owner/repo"}, Asset: "plugin-*.zip", Format: "zip", Mappings: []Mapping{{From: "addons", To: "addons"}}}
}

func TestHTTPSourceGitHubSelectsChannelAndExactPin(t *testing.T) {
	fixture := []map[string]any{
		{"id": 30, "tag_name": "v3.0.0-rc1", "prerelease": true, "published_at": "2026-10-20T00:00:00Z", "assets": []map[string]any{{"name": "plugin-linux.zip", "browser_download_url": "https://example.org/pre.zip"}}},
		{"id": 25, "tag_name": "v2.5.0", "draft": true, "assets": []map[string]any{{"name": "plugin-linux.zip", "browser_download_url": "https://example.org/draft.zip"}}},
		{"id": 20, "tag_name": "v2.0.0", "published_at": "2026-10-01T00:00:00Z", "assets": []map[string]any{{"name": "plugin-linux.zip", "browser_download_url": "https://example.org/stable.zip", "digest": "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"}}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "" {
			t.Error("GitHub received GitLab token")
		}
		switch r.URL.EscapedPath() {
		case "/repos/owner/repo/releases":
			json.NewEncoder(w).Encode(fixture)
		case "/repos/owner/repo/releases/tags/release%2Fcandidate":
			json.NewEncoder(w).Encode(map[string]any{"id": 7, "tag_name": "release/candidate", "prerelease": true, "assets": []map[string]any{{"name": "plugin-linux.zip", "browser_download_url": "https://example.org/pin.zip"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p := acquisitionPlugin()
	p.Source.URL = srv.URL
	source := HTTPSource{Client: srv.Client(), Credentials: map[string]string{"unused": "secret"}}
	release, err := source.Resolve(context.Background(), p)
	if err != nil || release.Version != "v2.0.0" || release.ID != "20" || release.Asset.SHA256 != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("stable = %#v, %v", release, err)
	}
	p.Prerelease = true
	release, err = source.Resolve(context.Background(), p)
	if err != nil || release.Version != "v3.0.0-rc1" {
		t.Fatalf("prerelease = %#v, %v", release, err)
	}
	p.Prerelease = false
	p.Pin = "release/candidate"
	release, err = source.Resolve(context.Background(), p)
	if err != nil || release.Version != p.Pin {
		t.Fatalf("pin = %#v, %v", release, err)
	}
	p.Pin = "missing"
	if _, err = source.Resolve(context.Background(), p); err == nil {
		t.Fatal("missing pin fell back to latest")
	}
}

func TestHTTPSourceRejectsAmbiguousAssetsAndDoesNotFallBack(t *testing.T) {
	for _, assets := range [][]map[string]string{
		{{"name": "plugin-linux.zip", "browser_download_url": "https://example.org/one"}, {"name": "plugin-windows.zip", "browser_download_url": "https://example.org/two"}},
		{{"name": "unmatched.zip", "browser_download_url": "https://example.org/one"}},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode([]any{map[string]any{"id": 2, "tag_name": "v2", "assets": assets}, map[string]any{"id": 1, "tag_name": "v1", "assets": []map[string]string{{"name": "plugin-old.zip", "browser_download_url": "https://example.org/old"}}}})
		}))
		p := acquisitionPlugin()
		p.Source.URL = srv.URL
		if release, err := (HTTPSource{Client: srv.Client()}).Resolve(context.Background(), p); err == nil {
			t.Errorf("accepted invalid latest layout: %#v", release)
		}
		srv.Close()
	}
}

func TestHTTPSourceGitLabReleaseLinkAndCredential(t *testing.T) {
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "private-secret" {
			t.Error("missing private GitLab token")
		}
		switch r.URL.EscapedPath() {
		case "/gitlab/api/v4/projects/group%2Fsub%2Frepo/releases":
			json.NewEncoder(w).Encode([]any{
				map[string]any{"tag_name": "v4.0.0", "upcoming_release": true, "assets": map[string]any{"links": []any{}}},
				map[string]any{"tag_name": "v3.0.0-rc1", "assets": map[string]any{"links": []any{}}},
				map[string]any{"tag_name": "v2.0.0", "commit": map[string]string{"id": "commit-id"}, "assets": map[string]any{"links": []map[string]any{{"id": 8, "name": "plugin-linux.zip", "url": base + "/api/v4/projects/7/jobs/9/artifacts", "direct_asset_url": base + "/release-download"}}}},
			})
		case "/api/v4/projects/7/jobs/9/artifacts":
			io.WriteString(w, "package")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	base = srv.URL
	p := acquisitionPlugin()
	p.Source = Source{Provider: "gitlab", URL: srv.URL + "/gitlab", Repository: "group/sub/repo", Credential: "gitlab-private"}
	source := HTTPSource{Client: srv.Client(), Credentials: map[string]string{"gitlab-private": "private-secret"}}
	release, err := source.Resolve(context.Background(), p)
	if err != nil || release.Version != "v2.0.0" || release.Asset.URL != base+"/api/v4/projects/7/jobs/9/artifacts" {
		t.Fatalf("release = %#v, %v", release, err)
	}
	dest := filepath.Join(t.TempDir(), "download.zip")
	if err := source.Download(context.Background(), p, release, dest); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "package" {
		t.Fatalf("download = %q", got)
	}
	source.Credentials = nil
	if _, err := source.Resolve(context.Background(), p); err == nil {
		t.Fatal("missing named credential accepted")
	}
}

func TestHTTPSourceStripsCredentialOnCrossOriginRedirects(t *testing.T) {
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "" || r.Header.Get("Authorization") != "" {
			t.Error("credential escaped GitLab origin")
		}
		io.WriteString(w, "safe")
	}))
	defer foreign.Close()
	var local string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "secret" {
			t.Errorf("same-origin request lacks token at %s", r.URL.Path)
		}
		if r.URL.Path == "/start" {
			http.Redirect(w, r, local+"/same", http.StatusFound)
			return
		}
		http.Redirect(w, r, foreign.URL+"/external", http.StatusFound)
	}))
	defer origin.Close()
	local = origin.URL
	p := acquisitionPlugin()
	p.Source = Source{Provider: "gitlab", URL: origin.URL, Repository: "group/repo", Credential: "private"}
	source := HTTPSource{Client: origin.Client(), Credentials: map[string]string{"private": "secret"}}
	for _, u := range []string{origin.URL + "/start", foreign.URL + "/direct"} {
		dest := filepath.Join(t.TempDir(), "asset")
		if err := source.Download(context.Background(), p, Release{Asset: Asset{URL: u}}, dest); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(dest)
		if string(b) != "safe" {
			t.Fatalf("download=%q", b)
		}
	}
}

type acquisitionTransport func(*http.Request) (*http.Response, error)

func (f acquisitionTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHTTPSourceErrorsDoNotExposeSecrets(t *testing.T) {
	p := acquisitionPlugin()
	p.Source = Source{Provider: "gitlab", URL: "https://gitlab.example.org", Repository: "group/repo", Credential: "private"}
	source := HTTPSource{Client: &http.Client{Transport: acquisitionTransport(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("transport exposed secret-token https://example.org/?key=query-secret")
	})}, Credentials: map[string]string{"private": "secret-token"}}
	for _, call := range []func() error{
		func() error { _, err := source.Resolve(context.Background(), p); return err },
		func() error {
			return source.Download(context.Background(), p, Release{Asset: Asset{URL: "https://example.org/file?key=query-secret"}}, filepath.Join(t.TempDir(), "asset"))
		},
	} {
		err := call()
		if err == nil {
			t.Fatal("failed transport succeeded")
		}
		if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "https://") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}

func TestHTTPSourceDownloadVerifiesDigestAndRemovesPartial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello") }))
	defer srv.Close()
	p := acquisitionPlugin()
	source := HTTPSource{Client: srv.Client()}
	sum := sha256.Sum256([]byte("hello"))
	release := Release{Asset: Asset{URL: srv.URL, SHA256: hex.EncodeToString(sum[:])}}
	dest := filepath.Join(t.TempDir(), "asset")
	if err := source.Download(context.Background(), p, release, dest); err != nil {
		t.Fatal(err)
	}
	release.Asset.SHA256 = strings.Repeat("0", 64)
	dest = filepath.Join(t.TempDir(), "bad")
	if err := source.Download(context.Background(), p, release, dest); err == nil {
		t.Fatal("bad digest accepted")
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("failed download remains: %v", err)
	}
}

func TestHTTPSourceDownloadRejectsOversizeAndExistingDestination(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(int64(300)<<20))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	p := acquisitionPlugin()
	source := HTTPSource{Client: srv.Client()}
	release := Release{Asset: Asset{URL: srv.URL}}
	dest := filepath.Join(t.TempDir(), "asset")
	if err := source.Download(context.Background(), p, release, dest); err == nil {
		t.Fatal("oversize download accepted")
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("oversize download remains: %v", err)
	}
	if err := os.WriteFile(dest, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := source.Download(context.Background(), p, release, dest); err == nil {
		t.Fatal("existing destination overwritten")
	}
	data, _ := os.ReadFile(dest)
	if string(data) != "original" {
		t.Fatalf("original changed: %q", data)
	}
}

func TestHTTPSourceMetadataLimitAndTrailingGarbage(t *testing.T) {
	for _, body := range []string{strings.Repeat(" ", 8<<20) + "[]", `[{"id":1,"tag_name":"v1","assets":[{"name":"plugin-linux.zip","browser_download_url":"https://example.org/a"}]}] garbage`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
		p := acquisitionPlugin()
		p.Source.URL = srv.URL
		if _, err := (HTTPSource{Client: srv.Client()}).Resolve(context.Background(), p); err == nil {
			t.Fatal("invalid release metadata accepted")
		}
		srv.Close()
	}
}

func TestHTTPSourcePaginationFindsStableRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			entries := make([]map[string]any, 100)
			for i := range entries {
				entries[i] = map[string]any{"id": i + 100, "tag_name": fmt.Sprintf("v2.0.0-rc%d", i), "prerelease": true}
			}
			json.NewEncoder(w).Encode(entries)
			return
		}
		json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "tag_name": "v1.0.0", "assets": []map[string]string{{"name": "plugin-linux.zip", "browser_download_url": "https://example.org/a"}}}})
	}))
	defer srv.Close()
	p := acquisitionPlugin()
	p.Source.URL = srv.URL
	release, err := (HTTPSource{Client: srv.Client()}).Resolve(context.Background(), p)
	if err != nil || release.Version != "v1.0.0" {
		t.Fatalf("paginated stable=%#v, %v", release, err)
	}
}

func TestHTTPSourceDoesNotDiscloseSignedURLInRedirectReferer(t *testing.T) {
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") != "" {
			t.Error("redirect disclosed signed source URL in Referer")
		}
		io.WriteString(w, "safe")
	}))
	defer foreign.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, foreign.URL, http.StatusFound) }))
	defer origin.Close()
	p := acquisitionPlugin()
	err := (HTTPSource{Client: origin.Client()}).Download(context.Background(), p, Release{Asset: Asset{URL: origin.URL + "/asset?signature=private-value"}}, filepath.Join(t.TempDir(), "asset"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestHTTPSourceRejectsLinkedDownloadParent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "hello") }))
	defer srv.Close()
	outside := t.TempDir()
	linked := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(outside, linked); err != nil {
		t.Fatal(err)
	}
	err := (HTTPSource{Client: srv.Client()}).Download(context.Background(), acquisitionPlugin(), Release{Asset: Asset{URL: srv.URL}}, filepath.Join(linked, "asset"))
	if err == nil {
		t.Fatal("linked download parent accepted")
	}
	files, _ := os.ReadDir(outside)
	if len(files) != 0 {
		t.Fatal("download wrote outside staging parent")
	}
}

func TestHTTPSourceCancellationDuringDownloadRemovesPartial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "partial")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	destination := filepath.Join(t.TempDir(), "asset")
	complete := make(chan error, 1)
	go func() {
		complete <- (HTTPSource{Client: srv.Client()}).Download(ctx, acquisitionPlugin(), Release{Asset: Asset{URL: srv.URL}}, destination)
	}()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-complete:
			t.Fatalf("download finished before cancellation: %v", err)
		case <-deadline:
			cancel()
			t.Fatal("download did not begin writing")
		case <-ticker.C:
			if info, err := os.Stat(destination); err == nil && info.Size() > 0 {
				cancel()
				err := <-complete
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation error = %v", err)
				}
				if _, err = os.Stat(destination); !os.IsNotExist(err) {
					t.Fatalf("canceled download remains: %v", err)
				}
				return
			}
		}
	}
}
