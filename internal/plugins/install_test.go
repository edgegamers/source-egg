package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testCode = "addons/sourcemod/plugins/custom.smx"

func installFixture(t *testing.T) Installer {
	t.Helper()
	return Installer{Volume: t.TempDir(), GameDir: "tf"}
}
func putInstallFile(t *testing.T, volume, name, data string) {
	t.Helper()
	full := filepath.Join(volume, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}
func installHash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func stagedFixture(t *testing.T, i Installer, version string, files map[string]string) Snapshot {
	t.Helper()
	id := installHash(version)
	s := Snapshot{ID: id, Version: version, Recipe: installHash("recipe"), Directory: ".source/plugins/packages/" + id, Files: map[string]File{}}
	for name, data := range files {
		putInstallFile(t, i.Volume, s.Directory+"/"+name, data)
		s.Files[name] = File{SHA256: installHash(data), Seed: Protected(name, nil)}
	}
	return s
}
func assertInstallFile(t *testing.T, i Installer, name, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(i.Volume, "tf", name))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}
func assertInstallMissing(t *testing.T, i Installer, name string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(i.Volume, "tf", name)); !os.IsNotExist(err) {
		t.Fatalf("%s unexpectedly exists: %v", name, err)
	}
}
func writeInstallJSON(t *testing.T, i Installer, name string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	putInstallFile(t, i.Volume, name, string(b))
}

func TestInstallerAppliesStagesAndPreservesSeedFiles(t *testing.T) {
	i := installFixture(t)
	putInstallFile(t, i.Volume, "tf/cfg/custom.cfg", "my settings")
	s := stagedFixture(t, i, "v1", map[string]string{testCode: "code v1", "cfg/custom.cfg": "defaults", "addons/sourcemod/data/new.txt": "seed data"})
	changes, err := i.Preview("custom", &s, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 {
		t.Fatalf("preview: %#v", changes)
	}
	if err = i.Apply("custom", s, false); err != nil {
		t.Fatal(err)
	}
	assertInstallFile(t, i, testCode, "code v1")
	assertInstallFile(t, i, "cfg/custom.cfg", "my settings")
	assertInstallFile(t, i, "addons/sourcemod/data/new.txt", "seed data")
	installed, err := i.Installed()
	if err != nil {
		t.Fatal(err)
	}
	if installed["custom"].Current.ID != s.ID {
		t.Fatalf("receipt: %#v", installed)
	}
	if err = i.Recover(); err != nil {
		t.Fatal(err)
	}
}

func TestInstallerRejectsChangedStageBeforeAnyWrite(t *testing.T) {
	i := installFixture(t)
	s := stagedFixture(t, i, "v1", map[string]string{"addons/a.smx": "first", "addons/z.smx": "second"})
	putInstallFile(t, i.Volume, s.Directory+"/addons/z.smx", "tampered")
	if err := i.Apply("custom", s, false); err == nil {
		t.Fatal("accepted a changed staged file")
	}
	assertInstallMissing(t, i, "addons/a.smx")
	installed, err := i.Installed()
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 0 {
		t.Fatal("committed a failed package")
	}
}

func TestInstallerProtectsUnownedAndFrameworkFiles(t *testing.T) {
	for _, kind := range []string{"unowned", "framework-files", "framework-stock", "other-plugin"} {
		t.Run(kind, func(t *testing.T) {
			i := installFixture(t)
			first := stagedFixture(t, i, "first", map[string]string{testCode: "other code"})
			if kind == "other-plugin" {
				if err := i.Apply("other", first, false); err != nil {
					t.Fatal(err)
				}
			} else {
				putInstallFile(t, i.Volume, "tf/"+testCode, "other code")
				if kind == "framework-files" {
					writeInstallJSON(t, i, ".source/framework-sourcemod.json", map[string]any{"version": "1", "files": []string{"tf/" + testCode}})
				}
				if kind == "framework-stock" {
					writeInstallJSON(t, i, ".source/framework-sourcemod.json", map[string]any{"version": "1", "stock": map[string]string{"tf/" + testCode: installHash("other code")}})
				}
			}
			next := stagedFixture(t, i, "next", map[string]string{testCode: "replacement"})
			if err := i.Apply("custom", next, true); err == nil {
				t.Fatal("overwrote another owner's file")
			}
			assertInstallFile(t, i, testCode, "other code")
		})
	}
}

func TestInstallerRequiresExplicitReplacementOfLocalEdits(t *testing.T) {
	i := installFixture(t)
	first := stagedFixture(t, i, "first", map[string]string{testCode: "original"})
	if err := i.Apply("custom", first, false); err != nil {
		t.Fatal(err)
	}
	putInstallFile(t, i.Volume, "tf/"+testCode, "local edit")
	next := stagedFixture(t, i, "next", map[string]string{testCode: "updated"})
	if err := i.Apply("custom", next, false); err == nil {
		t.Fatal("replaced local edit without explicit choice")
	}
	assertInstallFile(t, i, testCode, "local edit")
	if err := i.Apply("custom", next, true); err != nil {
		t.Fatal(err)
	}
	assertInstallFile(t, i, testCode, "updated")
}

func TestInstallerAdoptsOnlyMatchingCompleteManagedFiles(t *testing.T) {
	for _, kind := range []string{"missing", "different", "matching"} {
		t.Run(kind, func(t *testing.T) {
			i := installFixture(t)
			s := stagedFixture(t, i, "v1", map[string]string{testCode: "known version", "cfg/custom.cfg": "defaults"})
			if kind != "missing" {
				data := "known version"
				if kind == "different" {
					data = "unknown version"
				}
				putInstallFile(t, i.Volume, "tf/"+testCode, data)
			}
			err := i.Adopt("custom", s)
			if kind != "matching" {
				if err == nil {
					t.Fatal("adopted unknown or absent code")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertInstallFile(t, i, testCode, "known version")
			assertInstallMissing(t, i, "cfg/custom.cfg")
			installed, err := i.Installed()
			if err != nil {
				t.Fatal(err)
			}
			if installed["custom"].Current.ID != s.ID {
				t.Fatal("missing adoption receipt")
			}
		})
	}
}

func TestInstallerUninstallPreservesLocalEditsAndSeedFiles(t *testing.T) {
	i := installFixture(t)
	s := stagedFixture(t, i, "v1", map[string]string{testCode: "original", "addons/other.smx": "unchanged", "cfg/custom.cfg": "defaults"})
	if err := i.Apply("custom", s, false); err != nil {
		t.Fatal(err)
	}
	putInstallFile(t, i.Volume, "tf/"+testCode, "local edit")
	if _, err := i.Preview("custom", nil, false); err != nil {
		t.Fatal(err)
	}
	if err := i.Uninstall("custom"); err != nil {
		t.Fatal(err)
	}
	assertInstallFile(t, i, testCode, "local edit")
	assertInstallFile(t, i, "cfg/custom.cfg", "defaults")
	assertInstallMissing(t, i, "addons/other.smx")
	installed, err := i.Installed()
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 0 {
		t.Fatal("uninstall kept ownership")
	}
}

func TestInstallerRetainsTwoPreviousSnapshotsForRollback(t *testing.T) {
	i := installFixture(t)
	for _, v := range []string{"v1", "v2", "v3", "v4"} {
		s := stagedFixture(t, i, v, map[string]string{testCode: v, "cfg/custom.cfg": v})
		if err := i.Apply("custom", s, false); err != nil {
			t.Fatal(err)
		}
	}
	installed, err := i.Installed()
	if err != nil {
		t.Fatal(err)
	}
	prior := installed["custom"].Previous
	if len(prior) != 2 || prior[0].Version != "v3" || prior[1].Version != "v2" {
		t.Fatalf("history: %#v", prior)
	}
	if err := i.Apply("custom", prior[0], false); err != nil {
		t.Fatal(err)
	}
	assertInstallFile(t, i, testCode, "v3")
	assertInstallFile(t, i, "cfg/custom.cfg", "v1")
}

func TestInstallerRejectsSymlinkParentsAndNonregularTargets(t *testing.T) {
	for _, kind := range []string{"parent", "file", "directory", "metadata"} {
		t.Run(kind, func(t *testing.T) {
			i := installFixture(t)
			s := stagedFixture(t, i, "v1", map[string]string{testCode: "code"})
			outside := t.TempDir()
			target := filepath.Join(i.Volume, "tf", filepath.FromSlash(testCode))
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "parent":
				target = filepath.Dir(target)
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, target); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.Symlink(filepath.Join(outside, "file"), target); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(target, 0755); err != nil {
					t.Fatal(err)
				}
			case "metadata":
				if err := os.Symlink(filepath.Join(outside, "receipt"), filepath.Join(i.Volume, ".source/plugins/installed.json")); err != nil {
					t.Fatal(err)
				}
			}
			if err := i.Apply("custom", s, true); err == nil {
				t.Fatal("accepted unsafe target")
			}
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatal("wrote through symlink")
			}
		})
	}
}

func TestInstallerAtomicReplacementDoesNotChangeHardlinkSource(t *testing.T) {
	i := installFixture(t)
	first := stagedFixture(t, i, "first", map[string]string{testCode: "original"})
	if err := i.Apply("custom", first, false); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(t.TempDir(), "shared")
	if err := os.Link(filepath.Join(i.Volume, "tf", testCode), shared); err != nil {
		t.Fatal(err)
	}
	next := stagedFixture(t, i, "next", map[string]string{testCode: "updated"})
	if err := i.Apply("custom", next, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(shared)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatal("changed shared hardlink source")
	}
	assertInstallFile(t, i, testCode, "updated")
}

func TestInstallerRejectsMalformedReceiptsWithoutMutation(t *testing.T) {
	for _, data := range []string{"{", "null", `{"version":1,"plugins":{"bad":{"current":{"id":"../escape"}}}}`, `{"version":2,"plugins":{}}`} {
		t.Run(data, func(t *testing.T) {
			i := installFixture(t)
			s := stagedFixture(t, i, "v1", map[string]string{testCode: "code"})
			putInstallFile(t, i.Volume, ".source/plugins/installed.json", data)
			if err := i.Apply("custom", s, false); err == nil {
				t.Fatal("accepted malformed receipt")
			}
			assertInstallMissing(t, i, testCode)
		})
	}
}

func TestInstallerRecoveryRejectsMalformedJournal(t *testing.T) {
	i := installFixture(t)
	putInstallFile(t, i.Volume, ".source/plugins/journal.json", `{"version":1,"undo":[{"path":"../escape"}]}`)
	if err := i.Recover(); !errors.Is(err, ErrRecovery) {
		t.Fatalf("expected unsafe recovery error, got %v", err)
	}
}

func TestInstallerFrameworkDisabledStockPathStillOwned(t *testing.T) {
	i := installFixture(t)
	name := "addons/sourcemod/plugins/disabled/custom.smx"
	writeInstallJSON(t, i, ".source/framework-sourcemod.json", map[string]any{"version": "1", "stock": map[string]string{"tf/" + testCode: installHash("stock")}})
	s := stagedFixture(t, i, "v1", map[string]string{name: "stock"})
	if err := i.Apply("custom", s, false); err == nil {
		t.Fatal("claimed disabled framework plugin")
	}
	assertInstallMissing(t, i, name)
}

func TestInstallerRejectsInvalidSnapshots(t *testing.T) {
	for _, kind := range []string{"id", "directory", "target", "hash", "empty"} {
		t.Run(kind, func(t *testing.T) {
			i := installFixture(t)
			s := stagedFixture(t, i, "v1", map[string]string{testCode: "code"})
			switch kind {
			case "id":
				s.ID = "../escape"
			case "directory":
				s.Directory = ".source/plugins/other"
			case "target":
				s.Files = map[string]File{"../escape": {SHA256: installHash("code")}}
			case "hash":
				s.Files[testCode] = File{SHA256: strings.Repeat("z", 64)}
			case "empty":
				s.Files = map[string]File{}
			}
			if err := i.Apply("custom", s, false); err == nil {
				t.Fatal("accepted invalid snapshot")
			}
		})
	}
}

func TestInstallerRetainsOwnershipWhenManyRecipesStopTracking(t *testing.T) {
	i := installFixture(t)
	installed := map[string]Installation{}
	for n := 0; n < 100; n++ {
		id := fmt.Sprintf("old%d", n)
		snapshotID := installHash(id)
		installed[id] = Installation{Current: Snapshot{ID: snapshotID, Version: "v1", Recipe: installHash("recipe"), Directory: ".source/plugins/packages/" + snapshotID, Files: map[string]File{"addons/" + id + ".smx": {SHA256: installHash(id)}}}}
	}
	writeInstallJSON(t, i, installedPath, installationReceipt{Version: 1, Plugins: installed})
	next := stagedFixture(t, i, "new", map[string]string{testCode: "new plugin"})
	if err := i.Apply("custom", next, false); err != nil {
		t.Fatal(err)
	}
	loaded, err := i.Installed()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 101 {
		t.Fatalf("lost old ownership: %d", len(loaded))
	}
}

func TestInstallerPreserveChoiceImmediatelyProtectsRollbackAndUninstall(t *testing.T) {
	i := installFixture(t)
	first := stagedFixture(t, i, "first", map[string]string{testCode: "original", "addons/other.smx": "other original"})
	next := stagedFixture(t, i, "next", map[string]string{testCode: "updated", "addons/other.smx": "other updated"})
	if err := i.Apply("custom", first, false); err != nil {
		t.Fatal(err)
	}
	if err := i.Apply("custom", next, false); err != nil {
		t.Fatal(err)
	}
	i.Preserve = map[string][]string{"custom": {testCode}}
	putInstallFile(t, i.Volume, "tf/"+testCode, "local edit")
	if err := i.Apply("custom", first, false); err != nil {
		t.Fatal(err)
	}
	assertInstallFile(t, i, testCode, "local edit")
	assertInstallFile(t, i, "addons/other.smx", "other original")
	// Preserve intent survives even if this Installer instance has no policy map.
	i.Preserve = nil
	if err := i.Uninstall("custom"); err != nil {
		t.Fatal(err)
	}
	assertInstallFile(t, i, testCode, "local edit")
	assertInstallMissing(t, i, "addons/other.smx")
}
