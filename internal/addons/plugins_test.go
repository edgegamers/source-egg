package addons

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpgradePreservesManuallyDisabledStockPlacementAndUpdatesBinary(t *testing.T) {
	vol := t.TempDir()
	first := filepath.Join(t.TempDir(), "first.tgz")
	second := filepath.Join(t.TempDir(), "second.tgz")
	os.WriteFile(first, archive(t, map[string]string{"addons/sourcemod/plugins/basechat.smx": "v1"}), 0600)
	os.WriteFile(second, archive(t, map[string]string{"addons/sourcemod/plugins/basechat.smx": "v2"}), 0600)
	if err := Deploy(first, vol, "cstrike", "sourcemod", "1"); err != nil {
		t.Fatal(err)
	}
	plugins := filepath.Join(vol, "cstrike/addons/sourcemod/plugins")
	os.MkdirAll(filepath.Join(plugins, "disabled"), 0755)
	os.Rename(filepath.Join(plugins, "basechat.smx"), filepath.Join(plugins, "disabled/basechat.smx"))
	if err := Deploy(second, vol, "cstrike", "sourcemod", "2"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(plugins, "disabled/basechat.smx"))
	if err != nil || string(got) != "v2" {
		t.Fatalf("disabled stock not upgraded: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(plugins, "basechat.smx")); !os.IsNotExist(err) {
		t.Fatal("reenabled disabled plugin")
	}
}

func TestUpgradeRejectsDuplicateStockPluginIdentity(t *testing.T) {
	vol := t.TempDir()
	src := filepath.Join(t.TempDir(), "src.tgz")
	os.WriteFile(src, archive(t, map[string]string{"addons/sourcemod/plugins/basechat.smx": "stock"}), 0600)
	if err := Deploy(src, vol, "cstrike", "sourcemod", "1"); err != nil {
		t.Fatal(err)
	}
	plugins := filepath.Join(vol, "cstrike/addons/sourcemod/plugins")
	os.MkdirAll(filepath.Join(plugins, "disabled"), 0755)
	os.WriteFile(filepath.Join(plugins, "disabled/basechat.smx"), []byte("custom"), 0600)
	if err := Deploy(src, vol, "cstrike", "sourcemod", "2"); err == nil {
		t.Fatal("accepted active/disabled duplicates")
	}
}

func TestPluginSwitchesEnableDisableRestoreAndPreserveCustomFiles(t *testing.T) {
	vol := t.TempDir()
	src := filepath.Join(t.TempDir(), "src.tgz")
	os.WriteFile(src, archive(t, map[string]string{"addons/sourcemod/plugins/basechat.smx": "stock", "addons/sourcemod/plugins/disabled/nominations.smx": "nominate"}), 0600)
	choices := map[string]string{"basechat": "0", "nominations": "1"}
	if err := Deploy(src, vol, "cstrike", "sourcemod", "1", choices); err != nil {
		t.Fatal(err)
	}
	plugins := filepath.Join(vol, "cstrike/addons/sourcemod/plugins")
	for _, name := range []string{"disabled/basechat.smx", "nominations.smx"} {
		if _, err := os.Stat(filepath.Join(plugins, name)); err != nil {
			t.Fatalf("missing switched plugin %s: %v", name, err)
		}
	}
	choices = map[string]string{"basechat": "1", "nominations": "0"}
	os.Remove(filepath.Join(plugins, "disabled/basechat.smx"))
	if err := Deploy(src, vol, "cstrike", "sourcemod", "2", choices); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"basechat.smx", "disabled/nominations.smx"} {
		if _, err := os.Stat(filepath.Join(plugins, name)); err != nil {
			t.Fatalf("missing restored/switched plugin %s: %v", name, err)
		}
	}
	os.WriteFile(filepath.Join(plugins, "basechat.smx"), []byte("my custom plugin"), 0600)
	if err := Deploy(src, vol, "cstrike", "sourcemod", "3", map[string]string{"basechat": "0"}); err == nil {
		t.Fatal("explicit switch silently replaced custom plugin")
	}
	b, _ := os.ReadFile(filepath.Join(plugins, "basechat.smx"))
	if string(b) != "my custom plugin" {
		t.Fatal("lost custom plugin")
	}
}

func TestRemovedStockPluginIsPrunedFromItsCurrentPlacement(t *testing.T) {
	for _, tc := range []struct {
		name, member, plugin, policy, placed string
	}{
		{"enabled", "disabled/nominations.smx", "nominations", "1", "nominations.smx"},
		{"disabled", "basechat.smx", "basechat", "0", "disabled/basechat.smx"},
	} {
		for _, edited := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/stock", true: "/custom"}[edited], func(t *testing.T) {
				vol, packages := t.TempDir(), t.TempDir()
				first, second := filepath.Join(packages, "first.tgz"), filepath.Join(packages, "second.tgz")
				if err := os.WriteFile(first, archive(t, map[string]string{"addons/sourcemod/plugins/" + tc.member: "stock-v1"}), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(second, archive(t, map[string]string{"addons/sourcemod/bin/server.so": "runtime-v2"}), 0600); err != nil {
					t.Fatal(err)
				}
				choices := map[string]string{tc.plugin: tc.policy}
				if err := Deploy(first, vol, "cstrike", "sourcemod", "1", choices); err != nil {
					t.Fatal(err)
				}
				plugins := filepath.Join(vol, "cstrike/addons/sourcemod/plugins")
				placed := filepath.Join(plugins, tc.placed)
				if edited {
					if err := os.WriteFile(placed, []byte("my custom replacement"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				// A new private replacement at the old location is not owned by
				// the receipt, even when a stock file remains at its alternate.
				if err := os.MkdirAll(filepath.Dir(filepath.Join(plugins, tc.member)), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(plugins, tc.member), []byte("private plugin"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := Deploy(second, vol, "cstrike", "sourcemod", "2", choices); err != nil {
					t.Fatal(err)
				}
				if edited {
					body, err := os.ReadFile(placed)
					if err != nil || string(body) != "my custom replacement" {
						t.Fatalf("removed edited plugin: %q, %v", body, err)
					}
				} else if _, err := os.Stat(placed); !os.IsNotExist(err) {
					t.Fatalf("orphaned removed stock at %s: %v", tc.placed, err)
				}
				body, err := os.ReadFile(filepath.Join(plugins, tc.member))
				if err != nil || string(body) != "private plugin" {
					t.Fatalf("removed private plugin: %q, %v", body, err)
				}
			})
		}
	}
}

func TestArchivePlacementChangeRetainsOwnershipThroughLaterRemoval(t *testing.T) {
	vol, packages := t.TempDir(), t.TempDir()
	choices := map[string]string{"nominations": "1"}
	for i, files := range []map[string]string{
		{"addons/sourcemod/plugins/disabled/nominations.smx": "stock-v1"},
		{"addons/sourcemod/plugins/nominations.smx": "stock-v2"},
		{"addons/sourcemod/bin/server.so": "runtime-v3"},
	} {
		pkg := filepath.Join(packages, []string{"first.tgz", "second.tgz", "third.tgz"}[i])
		if err := os.WriteFile(pkg, archive(t, files), 0600); err != nil {
			t.Fatal(err)
		}
		if err := Deploy(pkg, vol, "cstrike", "sourcemod", "update", choices); err != nil {
			t.Fatal(err)
		}
		active := filepath.Join(vol, "cstrike/addons/sourcemod/plugins/nominations.smx")
		if i == 1 {
			body, err := os.ReadFile(active)
			if err != nil || string(body) != "stock-v2" {
				t.Fatalf("lost stock ownership after archive moved member: %q, %v", body, err)
			}
		}
		if i == 2 {
			if _, err := os.Stat(active); !os.IsNotExist(err) {
				t.Fatalf("orphaned plugin after archive relocation and removal: %v", err)
			}
		}
	}
}

func TestRemovedStockCleanupRefusesRedirectedPluginDirectory(t *testing.T) {
	vol, packages := t.TempDir(), t.TempDir()
	first, second := filepath.Join(packages, "first.tgz"), filepath.Join(packages, "second.tgz")
	if err := os.WriteFile(first, archive(t, map[string]string{"addons/sourcemod/plugins/disabled/nominations.smx": "stock"}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, archive(t, map[string]string{"addons/sourcemod/bin/server.so": "runtime-v2"}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Deploy(first, vol, "cstrike", "sourcemod", "1", map[string]string{"nominations": "1"}); err != nil {
		t.Fatal(err)
	}
	plugins := filepath.Join(vol, "cstrike/addons/sourcemod/plugins")
	saved := filepath.Join(vol, "private-plugins")
	if err := os.Rename(plugins, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../../private-plugins", plugins); err != nil {
		t.Fatal(err)
	}
	if err := Deploy(second, vol, "cstrike", "sourcemod", "2"); err == nil {
		t.Fatal("followed redirected plugin directory during removed-stock cleanup")
	}
	data, err := os.ReadFile(filepath.Join(saved, "nominations.smx"))
	if err != nil || string(data) != "stock" {
		t.Fatalf("removed file through symlink: %q, %v", data, err)
	}
}
