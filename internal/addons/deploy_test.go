package addons

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployWithoutSystemTemp(t *testing.T) {
	for _, name := range []string{"metamod", "sourcemod"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			volume := filepath.Join(dir, "volume")
			if err := os.Mkdir(volume, 0700); err != nil {
				t.Fatal(err)
			}
			member := "addons/" + name + "/bin/server.so"
			pkg := filepath.Join(dir, "package.tar.gz")
			if err := os.WriteFile(pkg, archive(t, map[string]string{member: "runtime"}), 0600); err != nil {
				t.Fatal(err)
			}
			// Deployment must work even when the system temp filesystem cannot
			// accept any directories, independently of the archive's size.
			t.Setenv("TMPDIR", filepath.Join(dir, "unavailable"))
			if err := Deploy(pkg, volume, "cstrike", name, "test"); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(volume, "cstrike", member))
			if err != nil || string(data) != "runtime" {
				t.Fatalf("deployed runtime = %q, %v", data, err)
			}
			if _, err := os.Stat(filepath.Join(volume, ".source", "framework-"+name+".json")); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(volume)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() != ".source" && entry.Name() != "cstrike" {
					t.Fatalf("deployment left temporary files: %s", entry.Name())
				}
			}
		})
	}
}

func TestDeployCleansStagingAfterExtractionFailure(t *testing.T) {
	dir := t.TempDir()
	volume := filepath.Join(dir, "volume")
	if err := os.Mkdir(volume, 0700); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(dir, "bad.tar.gz")
	if err := os.WriteFile(pkg, archive(t, map[string]string{"../escape": "bad"}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", filepath.Join(dir, "unavailable"))
	if err := Deploy(pkg, volume, "cstrike", "sourcemod", "test"); err == nil || !strings.Contains(err.Error(), "unsafe archive path") {
		t.Fatalf("expected archive traversal rejection, got %v", err)
	}
	entries, err := os.ReadDir(volume)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed extraction left files in the volume: %v", entries)
	}
}
