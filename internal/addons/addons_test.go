package addons

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func archive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if e := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(body))}); e != nil {
			t.Fatal(e)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}
func TestResolveFiltersChannelAndRequiresExactPin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"tag_name":"2.0.0.10","prerelease":true,"assets":[{"name":"mmsource-2.0.0-git10-linux.tar.gz","browser_download_url":"https://github.com/alliedmodders/metamod-source/releases/download/2.0.0.10/mm.tar.gz"}]},{"tag_name":"1.12.0.4","prerelease":false,"assets":[{"name":"mmsource-1.12.0-git4-linux.tar.gz","browser_download_url":"https://github.com/alliedmodders/metamod-source/releases/download/1.12.0.4/mm.tar.gz"}]}]`)
	}))
	defer srv.Close()
	r := Resolver{HTTP: srv.Client(), API: srv.URL}
	p, e := r.Resolve(context.Background(), "metamod", "")
	if e != nil {
		t.Fatal(e)
	}
	if p.Version != "1.12.0.4" {
		t.Fatal(p)
	}
	if _, e = r.Resolve(context.Background(), "metamod", "1.12.0.999"); e == nil {
		t.Fatal("missing pin became latest")
	}
	if _, e = r.Resolve(context.Background(), "arbitrary-repo", ""); e == nil {
		t.Fatal("allowed unregistered provider")
	}
}
func TestCacheCoalescesDownloads(t *testing.T) {
	var hits atomic.Int32
	data := archive(t, map[string]string{"addons/metamod/bin/server.so": "binary"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write(data) }))
	defer srv.Close()
	c := NewCache(t.TempDir(), srv.Client())
	c.AllowURL = func(s string) bool { return strings.HasPrefix(s, srv.URL+"/") }
	pkg := Package{Name: "metamod", Version: "1.12.0.4", URL: srv.URL + "/mm.tar.gz"}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, e := c.Fetch(context.Background(), pkg); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if hits.Load() != 1 {
		t.Fatal("duplicate downloads", hits.Load())
	}
}
func TestExtractionRejectsTraversalAndDeploymentPreservesPrivateFiles(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "bad.tgz")
	os.WriteFile(bad, archive(t, map[string]string{"../escape": "bad"}), 0600)
	if e := Extract(bad, filepath.Join(root, "unpack")); e == nil {
		t.Fatal("accepted archive traversal")
	}
	vol := filepath.Join(root, "volume")
	os.MkdirAll(filepath.Join(vol, "cstrike/addons/sourcemod/configs"), 0755)
	os.WriteFile(filepath.Join(vol, "cstrike/addons/sourcemod/configs/admins_simple.ini"), []byte("mine"), 0600)
	good := filepath.Join(root, "good.tgz")
	os.WriteFile(good, archive(t, map[string]string{"addons/sourcemod/configs/admins_simple.ini": "default", "addons/sourcemod/bin/server.so": "new"}), 0600)
	if e := Deploy(good, vol, "cstrike", "sourcemod", "1.13.0.1"); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(vol, "cstrike/addons/sourcemod/configs/admins_simple.ini"))
	if string(b) != "mine" {
		t.Fatal("overwrote private config")
	}
	b, _ = os.ReadFile(filepath.Join(vol, "cstrike/addons/sourcemod/bin/server.so"))
	if string(b) != "new" {
		t.Fatal("missing runtime")
	}
	outside := t.TempDir()
	os.RemoveAll(filepath.Join(vol, "cstrike/addons/sourcemod/bin"))
	os.Symlink(outside, filepath.Join(vol, "cstrike/addons/sourcemod/bin"))
	if e := Deploy(good, vol, "cstrike", "sourcemod", "1.13.0.2"); e == nil {
		t.Fatal("followed symlink outside volume")
	}
	if _, e := os.Stat(filepath.Join(outside, "server.so")); !os.IsNotExist(e) {
		t.Fatal("wrote outside volume")
	}
}

func TestUpgradeUpdatesStockPluginsAndExtensionsButPreservesCustomEdits(t *testing.T) {
	root := t.TempDir()
	vol := t.TempDir()
	first := filepath.Join(root, "one.tgz")
	second := filepath.Join(root, "two.tgz")
	files := map[string]string{"addons/sourcemod/plugins/admin.smx": "stock1", "addons/sourcemod/extensions/sdktools.ext.so": "stock1", "addons/sourcemod/plugins/customized.smx": "stock1", "addons/sourcemod/plugins/disabled.smx": "stock1"}
	os.WriteFile(first, archive(t, files), 0600)
	if err := Deploy(first, vol, "cstrike", "sourcemod", "1.13.0.1"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(vol, "cstrike/addons/sourcemod/plugins/customized.smx"), []byte("my edit"), 0600)
	os.Remove(filepath.Join(vol, "cstrike/addons/sourcemod/plugins/disabled.smx"))
	for k := range files {
		files[k] = "stock2"
	}
	os.WriteFile(second, archive(t, files), 0600)
	if err := Deploy(second, vol, "cstrike", "sourcemod", "1.13.0.2"); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"plugins/admin.smx": "stock2", "extensions/sdktools.ext.so": "stock2", "plugins/customized.smx": "my edit"} {
		data, _ := os.ReadFile(filepath.Join(vol, "cstrike/addons/sourcemod", path))
		if string(data) != want {
			t.Fatalf("%s=%s, want %s", path, data, want)
		}
	}
	if _, err := os.Stat(filepath.Join(vol, "cstrike/addons/sourcemod/plugins/disabled.smx")); !os.IsNotExist(err) {
		t.Fatal("re-enabled deleted/disabled stock plugin")
	}
}
