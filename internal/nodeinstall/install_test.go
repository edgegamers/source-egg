package nodeinstall

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/edgegamers/source-egg/internal/node"
)

func defaults(root string) node.Config {
	c := node.DefaultConfig()
	c.StateDir = filepath.Join(root, "state")
	c.SharedDir = filepath.Join(root, "shared")
	c.CacheDir = filepath.Join(root, "cache")
	c.SteamCMD = filepath.Join(root, "steam/steamcmd.sh")
	return c
}
func TestWizardConfiguresFeaturesScopesAndSelectedGames(t *testing.T) {
	base := defaults(t.TempDir())
	var out bytes.Buffer
	answers := &promptAnswers{out: &out, values: map[string]string{
		"Games to prepare now": "CS:S,TF2", "Game-update restart policy": "window",
		"Restart window in node local time": "01:00-02:00", "Maximum restart delay minutes": "30",
		"Restart countdown seconds": "60", "Game update check interval minutes": "20",
		"Dev join-password sharing": "node", "Password rotation schedule": "daily",
	}}
	got, err := Configure(base, answers, &out)
	if err != nil {
		t.Fatal(err)
	}
	if got.PasswordScope != "node" || got.PasswordIntervalHours != 24 || got.Restart.Mode != "window" || got.Restart.Window != "01:00-02:00" || got.CheckMinutes != 20 || !reflect.DeepEqual(got.PrefetchGames, []string{"css", "tf2"}) {
		t.Fatal(got)
	}
}
func TestHeadlessInstallPreservesExistingSettingsAndRestartsService(t *testing.T) {
	dir := t.TempDir()
	cfg := defaults(dir)
	cfg.PasswordScope = "server"
	cfg.NodeChannel = "beta"
	cfg.Webhook = "https://discord.com/api/webhooks/private/token"
	configPath := filepath.Join(dir, "etc/config.json")
	if err := SaveConfig(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "downloaded-node")
	os.WriteFile(source, []byte("verified binary"), 0755)
	var calls []string
	var output bytes.Buffer
	opts := Options{ConfigPath: configPath, BinaryPath: filepath.Join(dir, "bin/sourcenode"), UnitPath: filepath.Join(dir, "systemd/sourcenode.service"), Executable: source, Yes: true, Output: &output, Defaults: defaults(dir), Run: func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}, Provision: func(_ context.Context, path string) error {
		os.MkdirAll(filepath.Dir(path), 0755)
		return os.WriteFile(path, []byte("steam"), 0755)
	}}
	if err := Install(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	got, err := node.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.PasswordScope != "server" || got.NodeChannel != "beta" || got.Webhook != cfg.Webhook {
		t.Fatal("existing settings lost", got)
	}
	if !reflect.DeepEqual(calls, []string{"systemctl daemon-reload", "systemctl enable sourcenode", "systemctl restart sourcenode"}) {
		t.Fatal(calls)
	}
	info, _ := os.Stat(configPath)
	if info.Mode().Perm() != 0600 {
		t.Fatal("config not private")
	}
	binary, _ := os.ReadFile(opts.BinaryPath)
	if string(binary) != "verified binary" {
		t.Fatal("node not installed")
	}
	if strings.Contains(output.String(), "private/token") {
		t.Fatal("wizard printed a stored secret")
	}
}
func TestInterruptedWizardDoesNotChangeConfigOrInstallFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := defaults(dir)
	SaveConfig(path, cfg)
	before, _ := os.ReadFile(path)
	err := Install(context.Background(), Options{ConfigPath: path, BinaryPath: filepath.Join(dir, "bin/node"), UnitPath: filepath.Join(dir, "node.service"), Input: strings.NewReader(""), Output: &bytes.Buffer{}, Defaults: cfg})
	if err == nil {
		t.Fatal("EOF accepted as approval")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("config changed after cancelled setup")
	}
	if _, err = os.Stat(filepath.Join(dir, "bin/node")); !os.IsNotExist(err) {
		t.Fatal("installed after cancelled wizard")
	}
}
func TestInstallerReportsServiceFailure(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "node")
	os.WriteFile(source, []byte("binary"), 0755)
	err := Install(context.Background(), Options{ConfigPath: filepath.Join(dir, "etc/config.json"), BinaryPath: filepath.Join(dir, "bin/node"), UnitPath: filepath.Join(dir, "unit.service"), Executable: source, Yes: true, Defaults: defaults(dir), Output: &bytes.Buffer{}, Provision: func(context.Context, string) error { return nil }, Run: func(context.Context, string, ...string) error { return errors.New("service unavailable") }})
	if err == nil || !strings.Contains(err.Error(), "service") {
		t.Fatal("service failure hidden", err)
	}
}

func TestSavingConfigDoesNotChangeExistingParentPermissions(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0755)
	if err := SaveConfig(filepath.Join(dir, "node.json"), defaults(dir)); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0755 {
		t.Fatal("installer changed an existing parent directory's permissions")
	}
}

func TestUnsafeManagedDirectoriesFailBeforeProvisioning(t *testing.T) {
	for _, name := range []string{"cleaned-root", "symlink-root", "private-in-public"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := defaults(dir)
			switch name {
			case "cleaned-root":
				cfg.StateDir = "/tmp/.."
			case "symlink-root":
				link := filepath.Join(dir, "state-link")
				if err := os.Symlink("/", link); err != nil {
					t.Fatal(err)
				}
				cfg.StateDir = link
			case "private-in-public":
				cfg.SharedDir = dir
				cfg.StateDir = filepath.Join(dir, "private")
			}
			touched := false
			err := Install(context.Background(), Options{ConfigPath: filepath.Join(dir, "config.json"), Defaults: cfg, Yes: true, Output: &bytes.Buffer{}, Provision: func(context.Context, string) error {
				touched = true
				return errors.New("stop before filesystem changes")
			}})
			if err == nil || touched {
				t.Fatal("unsafe paths accepted before privileged setup", err)
			}
		})
	}
}
