package plugins

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func interruptedFixture(t *testing.T, i Installer, corrupt bool) {
	t.Helper()
	first := stagedFixture(t, i, "v1", map[string]string{testCode: "old code"})
	next := stagedFixture(t, i, "v2", map[string]string{testCode: "partial code", "addons/new.smx": "new code"})
	before := installationReceipt{Version: 1, Plugins: map[string]Installation{"custom": {Current: first}}}
	after := installationReceipt{Version: 1, Plugins: map[string]Installation{"custom": {Current: next, Previous: []Snapshot{first}}}}
	dir := ".source/plugins/transactions/" + strings.Repeat("a", 32)
	putInstallFile(t, i.Volume, dir+"/0", "old code")
	writeInstallJSON(t, i, dir+"/2", before)
	receiptBytes, err := os.ReadFile(filepath.Join(i.Volume, dir, "2"))
	if err != nil {
		t.Fatal(err)
	}
	putInstallFile(t, i.Volume, "tf/"+testCode, "partial code")
	putInstallFile(t, i.Volume, "tf/addons/new.smx", "new code")
	writeInstallJSON(t, i, installedPath, after)
	writeInstallJSON(t, i, journalPath, map[string]any{
		"version": 1, "game_dir": "tf", "directory": dir,
		"undo": []map[string]any{
			{"path": "tf/" + testCode, "existed": true, "backup": dir + "/0", "sha256": installHash("old code"), "mode": 0644},
			{"path": "tf/addons/new.smx", "existed": false},
			{"path": installedPath, "existed": true, "backup": dir + "/2", "sha256": installHash(string(receiptBytes)), "mode": 0600},
		},
	})
	if corrupt {
		putInstallFile(t, i.Volume, dir+"/2", "corrupt backup")
	}
}

func TestInstallerRecoversFilesAndReceiptAfterInterruptedCommit(t *testing.T) {
	i := installFixture(t)
	interruptedFixture(t, i, false)
	if err := i.Recover(); err != nil {
		t.Fatal(err)
	}
	assertInstallFile(t, i, testCode, "old code")
	assertInstallMissing(t, i, "addons/new.smx")
	installed, err := i.Installed()
	if err != nil {
		t.Fatal(err)
	}
	if installed["custom"].Current.Version != "v1" {
		t.Fatalf("receipt not rolled back: %#v", installed)
	}
	if _, err := os.Stat(filepath.Join(i.Volume, journalPath)); !os.IsNotExist(err) {
		t.Fatalf("journal not cleared: %v", err)
	}
	if err = i.Recover(); err != nil {
		t.Fatalf("second recovery: %v", err)
	}
}
func TestInstallerRecoveryPreflightsAllBackupsBeforeRestoring(t *testing.T) {
	i := installFixture(t)
	interruptedFixture(t, i, true)
	if err := i.Recover(); !errors.Is(err, ErrRecovery) {
		t.Fatalf("expected unsafe recovery, got %v", err)
	}
	assertInstallFile(t, i, testCode, "partial code")
	assertInstallFile(t, i, "addons/new.smx", "new code")
	if _, err := os.Stat(filepath.Join(i.Volume, journalPath)); err != nil {
		t.Fatalf("lost recovery evidence: %v", err)
	}
}
func TestInstallerFailedTransactionRestoresEarlierWrites(t *testing.T) {
	i := installFixture(t)
	first := stagedFixture(t, i, "v1", map[string]string{testCode: "old code"})
	if err := i.Apply("custom", first, false); err != nil {
		t.Fatal(err)
	}
	next := stagedFixture(t, i, "v2", map[string]string{testCode: "new code"})
	f, err := i.open()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// A disappeared stage after preflight is a real mid-deployment failure: the
	// first destination was replaced before opening the second source failed.
	err = i.transact(f, []fileMutation{
		{Path: "tf/" + testCode, BeforeSHA: installHash("old code"), Source: next.Directory + "/" + testCode, SHA256: installHash("new code")},
		{Path: "tf/addons/new.smx", Source: next.Directory + "/missing", SHA256: installHash("missing")},
	}, installationReceipt{Version: 1, Plugins: map[string]Installation{"custom": {Current: next}}})
	if err == nil {
		t.Fatal("expected transaction failure")
	}
	if errors.Is(err, ErrRecovery) {
		t.Fatalf("rollback unexpectedly incomplete: %v", err)
	}
	assertInstallFile(t, i, testCode, "old code")
	assertInstallMissing(t, i, "addons/new.smx")
	installed, err := i.Installed()
	if err != nil {
		t.Fatal(err)
	}
	if installed["custom"].Current.ID != first.ID {
		t.Fatal("receipt changed after failure")
	}
}
func TestInstallerRecoveryRejectsSymlinkBackupWithoutTouchingLiveFiles(t *testing.T) {
	i := installFixture(t)
	interruptedFixture(t, i, false)
	backup := filepath.Join(i.Volume, ".source/plugins/transactions", strings.Repeat("a", 32), "0")
	if err := os.Remove(backup); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "old")
	if err := os.WriteFile(elsewhere, []byte("old code"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, backup); err != nil {
		t.Fatal(err)
	}
	if err := i.Recover(); !errors.Is(err, ErrRecovery) {
		t.Fatalf("accepted symlink backup: %v", err)
	}
	assertInstallFile(t, i, testCode, "partial code")
}

func TestInstallerRefusesFilesChangedBetweenPlanningAndJournal(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(strconv.FormatBool(existing), func(t *testing.T) {
			i := installFixture(t)
			if existing {
				first := stagedFixture(t, i, "v1", map[string]string{testCode: "old code"})
				if err := i.Apply("custom", first, false); err != nil {
					t.Fatal(err)
				}
			}
			next := stagedFixture(t, i, "v2", map[string]string{testCode: "new code"})
			f, err := i.open()
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			installed, err := loadInstallations(f)
			if err != nil {
				t.Fatal(err)
			}
			_, mutations, err := i.plan(f, installed, "custom", &next, false, false)
			if err != nil {
				t.Fatal(err)
			}
			putInstallFile(t, i.Volume, "tf/"+testCode, "concurrent local edit")
			err = i.transact(f, mutations, installationReceipt{Version: 1, Plugins: map[string]Installation{"custom": {Current: next}}})
			if err == nil {
				t.Fatal("overwrote a file changed after planning")
			}
			assertInstallFile(t, i, testCode, "concurrent local edit")
		})
	}
}
