package nodeinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUninstallPreservesData(t *testing.T) {
	for _, purge := range []bool{false, true} {
		t.Run(map[bool]string{false: "retain config", true: "purge config only"}[purge], func(t *testing.T) {
			dir := t.TempDir()
			paths := map[string]string{}
			for _, name := range []string{"binary", "unit", "config", "state", "shared", "cache", "volume"} {
				p := filepath.Join(dir, name)
				if e := os.WriteFile(p, []byte("retain"), 0600); e != nil {
					t.Fatal(e)
				}
				paths[name] = p
			}
			var calls []string
			e := Uninstall(context.Background(), UninstallOptions{BinaryPath: paths["binary"], UnitPath: paths["unit"], ConfigPath: paths["config"], Purge: purge, Run: func(_ context.Context, name string, args ...string) error {
				calls = append(calls, name+" "+strings.Join(args, " "))
				return nil
			}})
			if e != nil {
				t.Fatal(e)
			}
			if strings.Join(calls, ";") != "systemctl stop sourcenode;systemctl disable sourcenode;systemctl daemon-reload" {
				t.Fatalf("commands=%v", calls)
			}
			for name, p := range paths {
				_, e := os.Stat(p)
				removed := name == "binary" || name == "unit" || (name == "config" && purge)
				if removed && !os.IsNotExist(e) {
					t.Fatal("not removed", name, e)
				}
				if !removed && e != nil {
					t.Fatal("data deleted", name, e)
				}
			}
		})
	}
}
func TestUninstallCommandFailureRetainsFiles(t *testing.T) {
	for _, fail := range []string{"stop", "disable"} {
		t.Run(fail, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "sourcenode")
			unit := filepath.Join(dir, "sourcenode.service")
			os.WriteFile(bin, []byte("binary"), 0755)
			os.WriteFile(unit, []byte("unit"), 0644)
			e := Uninstall(context.Background(), UninstallOptions{BinaryPath: bin, UnitPath: unit, Run: func(_ context.Context, _ string, args ...string) error {
				if args[0] == fail {
					return errors.New("injected command failure")
				}
				return nil
			}})
			if e == nil {
				t.Fatal("failure hidden")
			}
			for _, p := range []string{bin, unit} {
				if _, e := os.Stat(p); e != nil {
					t.Fatal("file removed after command failure", p)
				}
			}
		})
	}
}
func TestUninstallRejectsUnsafeTargetsBeforeCommands(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "keep")
	os.WriteFile(outside, []byte("keep"), 0600)
	link := filepath.Join(dir, "linked")
	if e := os.Symlink(outside, link); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"", "/", "relative", dir, link} {
		e := Uninstall(context.Background(), UninstallOptions{BinaryPath: path, UnitPath: filepath.Join(dir, "unit"), Run: func(context.Context, string, ...string) error {
			t.Fatal("unsafe paths reached command runner")
			return nil
		}})
		if e == nil {
			t.Fatal("unsafe path accepted", path)
		}
	}
	b, _ := os.ReadFile(outside)
	if string(b) != "keep" {
		t.Fatal("symlink target changed")
	}
}

func TestUninstallPinsTargetParentBeforeCommands(t *testing.T) {
	dir := t.TempDir()
	managed := filepath.Join(dir, "managed")
	outside := filepath.Join(dir, "outside")
	os.Mkdir(managed, 0755)
	os.Mkdir(outside, 0755)
	for _, p := range []string{filepath.Join(managed, "sourcenode"), filepath.Join(managed, "unit"), filepath.Join(outside, "sourcenode"), filepath.Join(outside, "unit")} {
		os.WriteFile(p, []byte("preserve"), 0600)
	}
	e := Uninstall(context.Background(), UninstallOptions{BinaryPath: filepath.Join(managed, "sourcenode"), UnitPath: filepath.Join(managed, "unit"), Run: func(_ context.Context, _ string, args ...string) error {
		if args[0] == "stop" {
			if e := os.Rename(managed, managed+"-old"); e != nil {
				t.Fatal(e)
			}
			if e := os.Symlink(outside, managed); e != nil {
				t.Fatal(e)
			}
		}
		return nil
	}})
	// The prevalidated root is pinned before service commands can change paths.
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"sourcenode", "unit"} {
		b, e := os.ReadFile(filepath.Join(outside, name))
		if e != nil || string(b) != "preserve" {
			t.Fatal("uninstall crossed swapped parent", name, e)
		}
		if _, e := os.Stat(filepath.Join(managed+"-old", name)); !os.IsNotExist(e) {
			t.Fatal("pinned managed file not removed", name, e)
		}
	}
}
