package content

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/edgegamers/source-egg/internal/game"
)

func TestUnsupportedHardlinksReturnExplicitErrorWithoutReplacingVPK(t *testing.T) {
	volume, err := os.MkdirTemp("/dev/shm", "source-delivery-test-")
	if err != nil {
		t.Skip("separate test filesystem unavailable:", err)
	}
	defer os.RemoveAll(volume)
	profile, _ := game.Lookup("css")
	id, _ := profile.Identity("public")
	store := &Store{Dir: t.TempDir(), Installer: &fixtureInstaller{build: "100"}}
	generation, err := store.Ensure(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	sourceInfo, _ := os.Stat(generation.Path)
	volumeInfo, _ := os.Stat(volume)
	if sourceInfo.Sys().(*syscall.Stat_t).Dev == volumeInfo.Sys().(*syscall.Stat_t).Dev {
		t.Skip("test paths share a filesystem")
	}
	if err := os.MkdirAll(filepath.Join(volume, "cstrike"), 0755); err != nil {
		t.Fatal(err)
	}
	stock := filepath.Join(volume, "cstrike/pak01_000.vpk")
	if err := os.WriteFile(stock, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	err = DeployWithOptions(generation, volume, store.Dir, DeployOptions{Method: "hardlink"})
	if !errors.Is(err, syscall.EXDEV) {
		t.Fatalf("cross-filesystem link did not fail explicitly: %v", err)
	}
	data, err := os.ReadFile(stock)
	if err != nil || string(data) != "keep" {
		t.Fatalf("failed hardlink replaced existing VPK: %q, %v", data, err)
	}
}

func TestAlternateDeliveryReplacesExistingSymlinkWithoutWritingSharedTarget(t *testing.T) {
	profile, _ := game.Lookup("css")
	id, _ := profile.Identity("public")
	store := &Store{Dir: t.TempDir(), Installer: &fixtureInstaller{build: "100"}}
	generation, err := store.Ensure(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"hardlink", "copy"} {
		t.Run(method, func(t *testing.T) {
			volume := t.TempDir()
			outside := filepath.Join(t.TempDir(), "stock")
			if err := os.WriteFile(outside, []byte("never overwrite"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(volume, "cstrike"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(volume, "cstrike/pak01_000.vpk")); err != nil {
				t.Fatal(err)
			}
			if err := DeployWithOptions(generation, volume, store.Dir, DeployOptions{Method: method}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(outside)
			if err != nil || string(data) != "never overwrite" {
				t.Fatalf("write followed symlink: %q, %v", data, err)
			}
		})
	}
}

// Removing method selection or replacing stock files in place breaks this test:
// old snapshots must remain intact and private files must survive every method.
func TestDeliveryMethodsKeepGenerationsAndPrivateFiles(t *testing.T) {
	for _, profile := range game.All() {
		for _, method := range []string{"symlink", "hardlink", "copy"} {
			t.Run(profile.Key+"/"+method, func(t *testing.T) {
				id, _ := profile.Identity("public")
				installer := &fixtureInstaller{build: "100"}
				store := &Store{Dir: t.TempDir(), Installer: installer}
				first, err := store.Ensure(context.Background(), id)
				if err != nil {
					t.Fatal(err)
				}
				volume := t.TempDir()
				if err := os.MkdirAll(filepath.Join(volume, profile.Dir, "cfg"), 0755); err != nil {
					t.Fatal(err)
				}
				private := filepath.Join(volume, profile.Dir, "cfg/server.cfg")
				if err := os.WriteFile(private, []byte("private"), 0600); err != nil {
					t.Fatal(err)
				}
				opts := DeployOptions{Method: method}
				if err := DeployWithOptions(first, volume, store.Dir, opts); err != nil {
					t.Fatal(err)
				}
				stock := filepath.Join(volume, profile.Dir, "pak01_000.vpk")
				info, err := os.Lstat(stock)
				if err != nil {
					t.Fatal(err)
				}
				if (info.Mode()&os.ModeSymlink != 0) != (method == "symlink") {
					t.Fatalf("incorrect VPK representation: %v", info.Mode())
				}
				sourceInfo, _ := os.Stat(filepath.Join(first.Path, profile.Dir, "pak01_000.vpk"))
				if method != "symlink" && os.SameFile(info, sourceInfo) != (method == "hardlink") {
					t.Fatal("incorrect VPK inode ownership")
				}
				installer.build = "200"
				second, err := store.Ensure(context.Background(), id)
				if err != nil {
					t.Fatal(err)
				}
				if err := DeployWithOptions(second, volume, store.Dir, opts); err != nil {
					t.Fatal(err)
				}
				for name, want := range map[string]string{stock: "chunk-200", filepath.Join(first.Path, profile.Dir, "pak01_000.vpk"): "chunk-100", private: "private"} {
					data, err := os.ReadFile(name)
					if err != nil || string(data) != want {
						t.Fatalf("%s = %q, %v; want %q", name, data, err, want)
					}
				}
			})
		}
	}
}

func TestDeliveryMethodsRejectEscapingVolumeDirectories(t *testing.T) {
	profile, _ := game.Lookup("css")
	id, _ := profile.Identity("public")
	store := &Store{Dir: t.TempDir(), Installer: &fixtureInstaller{build: "100"}}
	generation, err := store.Ensure(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"symlink", "hardlink", "copy"} {
		t.Run(method, func(t *testing.T) {
			volume, outside := t.TempDir(), t.TempDir()
			if err := os.Symlink(outside, filepath.Join(volume, "cstrike")); err != nil {
				t.Fatal(err)
			}
			if err := DeployWithOptions(generation, volume, store.Dir, DeployOptions{Method: method}); err == nil {
				t.Fatal("followed escaping directory")
			}
			entries, _ := os.ReadDir(outside)
			if len(entries) != 0 {
				t.Fatal("wrote outside volume")
			}
		})
	}
}

func TestHostHardlinkDeliveryRejectsServerOwnedSharedInodes(t *testing.T) {
	profile, _ := game.Lookup("css")
	id, _ := profile.Identity("public")
	store := &Store{Dir: t.TempDir(), Installer: &fixtureInstaller{build: "100"}}
	generation, err := store.Ensure(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := DeployWithOptions(generation, t.TempDir(), store.Dir, DeployOptions{Method: "hardlink", PreserveOwnership: true}); err == nil {
		t.Fatal("server owner could chmod and modify a shared inode")
	}
}

func TestHardlinkRejectsSharedInodesOwnedByServerProcess(t *testing.T) {
	profile, _ := game.Lookup("css")
	id, _ := profile.Identity("public")
	store := &Store{Dir: t.TempDir(), Installer: &fixtureInstaller{build: "100"}}
	generation, err := store.Ensure(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, uid := range []int{os.Geteuid(), 0} {
		if err := DeployWithOptions(generation, t.TempDir(), store.Dir, DeployOptions{Method: "hardlink", ServerUID: &uid}); err == nil {
			t.Fatalf("process UID %d can write or chmod shared inodes", uid)
		}
	}
}
