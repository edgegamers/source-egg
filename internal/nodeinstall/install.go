package nodeinstall

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/edgegamers/source-egg/internal/addons"
	"github.com/edgegamers/source-egg/internal/fsx"
	"github.com/edgegamers/source-egg/internal/node"
)

type Options struct {
	ConfigPath, BinaryPath, UnitPath, Executable, Channel string
	Yes                                                   bool
	Input                                                 io.Reader
	Output                                                io.Writer
	Defaults                                              node.Config
	Run                                                   func(context.Context, string, ...string) error
	Provision                                             func(context.Context, string) error
}

func SaveConfig(path string, c node.Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return fsx.JSON(root, filepath.Base(path), c, 0600)
}
func Install(ctx context.Context, o Options) error {
	if o.Output == nil {
		o.Output = io.Discard
	}
	if o.Input == nil {
		o.Input = os.Stdin
	}
	if o.ConfigPath == "" {
		o.ConfigPath = "/etc/sourcenode/config.json"
	}
	if o.BinaryPath == "" {
		o.BinaryPath = "/usr/local/bin/sourcenode"
	}
	if o.UnitPath == "" {
		o.UnitPath = "/etc/systemd/system/sourcenode.service"
	}
	for _, path := range []*string{&o.ConfigPath, &o.BinaryPath, &o.UnitPath} {
		absolute, err := filepath.Abs(*path)
		if err != nil {
			return err
		}
		*path = absolute
	}
	cfg := o.Defaults
	if cfg.StateDir == "" {
		cfg = node.DefaultConfig()
	}
	existing, err := node.LoadConfig(o.ConfigPath)
	if err == nil {
		cfg = existing
	} else if !os.IsNotExist(err) {
		return err
	}
	if o.Channel != "" {
		cfg.NodeChannel = o.Channel
	}
	if err = cfg.Validate(); err != nil {
		return err
	}
	if !o.Yes {
		cfg, err = Configure(cfg, o.Input, o.Output)
		if err != nil {
			return err
		}
	}
	if err = validateManagedDirectories(cfg); err != nil {
		return err
	}
	if o.Executable == "" {
		o.Executable, err = os.Executable()
		if err != nil {
			return err
		}
	}
	if o.Run == nil {
		o.Run = func(ctx context.Context, name string, args ...string) error {
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Stdout = o.Output
			cmd.Stderr = o.Output
			return cmd.Run()
		}
	}
	if o.Provision == nil {
		o.Provision = provisionSteam
	}
	if err = o.Provision(ctx, cfg.SteamCMD); err != nil {
		return fmt.Errorf("prepare host SteamCMD: %w", err)
	}
	for _, dir := range []string{cfg.StateDir, cfg.SharedDir, cfg.CacheDir} {
		if err = os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		mode := os.FileMode(0755)
		if dir == cfg.StateDir {
			mode = 0700
		}
		if err = os.Chmod(dir, mode); err != nil {
			return err
		}
	}
	// Install through atomic replacements; an existing running binary keeps its inode.
	if err = os.MkdirAll(filepath.Dir(o.BinaryPath), 0755); err != nil {
		return err
	}
	binaryRoot, err := os.OpenRoot(filepath.Dir(o.BinaryPath))
	if err != nil {
		return err
	}
	defer binaryRoot.Close()
	if err = fsx.Copy(binaryRoot, filepath.Base(o.BinaryPath), o.Executable, 0755); err != nil {
		return err
	}
	if err = SaveConfig(o.ConfigPath, cfg); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(o.UnitPath), 0755); err != nil {
		return err
	}
	unitRoot, err := os.OpenRoot(filepath.Dir(o.UnitPath))
	if err != nil {
		return err
	}
	defer unitRoot.Close()
	unit := "[Unit]\nDescription=Source Egg node service\nAfter=docker.service network-online.target\nWants=network-online.target\nRequires=docker.service\n\n[Service]\nType=simple\nExecStart=" + strconv.Quote(o.BinaryPath) + " -config " + strconv.Quote(o.ConfigPath) + " serve\nRestart=on-failure\nRestartSec=5\nUMask=0022\n\n[Install]\nWantedBy=multi-user.target\n"
	if err = fsx.Atomic(unitRoot, filepath.Base(o.UnitPath), []byte(unit), 0644); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "sourcenode"}, {"restart", "sourcenode"}} {
		if err = o.Run(ctx, "systemctl", args...); err != nil {
			return fmt.Errorf("systemd service setup failed: %w", err)
		}
	}
	fmt.Fprintf(o.Output, "Installed sourcenode (release channel: %s).\nConfig: %s\nShared files: %s\nFramework cache: %s\n", cfg.NodeChannel, o.ConfigPath, cfg.SharedDir, cfg.CacheDir)
	fmt.Fprintln(o.Output, "Generated join passwords remain opt-in per server.\nNext: sudo sourcenode status; sudo sourcenode doctor; journalctl -u sourcenode -f")
	return nil
}
func validateManagedDirectories(c node.Config) error {
	for _, dir := range append([]string{c.SharedDir, c.CacheDir}, c.PrivateDirectories()...) {
		clean := filepath.Clean(dir)
		switch clean {
		case "/", "/etc", "/var", "/var/lib", "/var/cache", "/usr", "/usr/local", "/srv", "/opt", "/home", "/root", "/tmp", "/run":
			return fmt.Errorf("use a dedicated child directory instead of system directory %s", dir)
		}
		for parent := clean; ; parent = filepath.Dir(parent) {
			info, err := os.Lstat(parent)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			if err == nil && info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("managed directory %s includes a symlink; use its real dedicated path", dir)
			}
			if err == nil && !info.IsDir() {
				return fmt.Errorf("managed directory %s is not a directory", dir)
			}
			if parent == "/" {
				break
			}
		}
	}
	private := filepath.Clean(c.StateDir)
	for _, public := range []string{c.SharedDir, c.CacheDir} {
		public = filepath.Clean(public)
		if private == public || strings.HasPrefix(private, public+"/") {
			return fmt.Errorf("private node state must be outside shared/cache directories")
		}
	}
	return nil
}
func provisionSteam(ctx context.Context, executable string) error {
	if _, err := os.Stat(executable); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if filepath.Base(executable) != "steamcmd.sh" {
		return fmt.Errorf("configure an existing steamcmd.sh path")
	}
	dir := filepath.Dir(executable)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://steamcdn-a.akamaihd.net/client/installer/steamcmd_linux.tar.gz", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("Steam bootstrap HTTP %d", resp.StatusCode)
	}
	temp, err := os.CreateTemp("", "source-steam-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	n, err := io.Copy(temp, io.LimitReader(resp.Body, (32<<20)+1))
	closeErr := temp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if n > 32<<20 {
		return fmt.Errorf("Steam bootstrap exceeds limit")
	}
	stage, err := os.MkdirTemp("", "source-steam-seed-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err = addons.Extract(temp.Name(), stage); err != nil {
		return err
	}
	if err = publishSteam(stage, dir); err != nil {
		return err
	}
	if _, err = os.Stat(executable); err != nil {
		return err
	}
	return nil
}
func publishSteam(stage, dest string) error {
	for _, file := range []string{"steamcmd.sh", "linux32/steamcmd"} {
		if info, err := os.Stat(filepath.Join(stage, file)); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("Steam bootstrap is incomplete: missing %s", file)
		}
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
	if err = filepath.WalkDir(stage, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(stage, path)
		if rel == "." || rel == "steamcmd.sh" {
			return nil
		}
		if d.IsDir() {
			return root.MkdirAll(rel, 0755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected Steam seed symlink")
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return fsx.Copy(root, rel, path, info.Mode().Perm())
	}); err != nil {
		return err
	}
	// Publish the launcher last, so interrupted setup can safely retry.
	return fsx.Copy(root, "steamcmd.sh", filepath.Join(stage, "steamcmd.sh"), 0755)
}
