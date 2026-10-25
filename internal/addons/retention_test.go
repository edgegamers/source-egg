package addons

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCacheRetentionProtectsPinnedAndUnknownArchives(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-60 * 24 * time.Hour)
	for i, version := range []string{"old", "pinned", "legacy"} {
		path := filepath.Join(dir, "archives", strings.Repeat(string(rune('a'+i)), 64))
		os.MkdirAll(path, 0755)
		file := filepath.Join(path, "package.tar.gz")
		os.WriteFile(file, []byte("archive"), 0644)
		os.Chtimes(file, old, old)
		if version != "legacy" {
			data, _ := json.Marshal(Package{Name: "metamod", Version: version})
			os.WriteFile(filepath.Join(path, "package.json"), data, 0644)
		}
	}
	cache := NewCache(dir, nil)
	n, e := cache.Prune(30*24*time.Hour, map[string]bool{"metamod:pinned": true})
	if e != nil || n != 1 {
		t.Fatal(n, e)
	}
	for _, letter := range []string{"b", "c"} {
		if _, e = os.Stat(filepath.Join(dir, "archives", strings.Repeat(letter, 64), "package.tar.gz")); e != nil {
			t.Fatal("protected cache archive removed", e)
		}
	}
}
