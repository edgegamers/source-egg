package nodeinstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

type UninstallOptions struct {
	BinaryPath, UnitPath, ConfigPath, ServiceName string
	Purge                                         bool
	Run                                           func(context.Context, string, ...string) error
}

// Uninstall removes only the explicitly named binary/unit and, with Purge,
// config file. It never recursively deletes a directory or touches game data.
func Uninstall(ctx context.Context, o UninstallOptions) error {
	if o.ServiceName == "" {
		o.ServiceName = "sourcenode"
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\.service)?$`).MatchString(o.ServiceName) {
		return errors.New("invalid node service name")
	}
	targets := []string{o.BinaryPath, o.UnitPath}
	if o.Purge {
		targets = append(targets, o.ConfigPath)
	}
	seen := map[string]bool{}
	for _, p := range targets {
		if e := uninstallTarget(p); e != nil {
			return e
		}
		p = filepath.Clean(p)
		if seen[p] {
			return errors.New("uninstall targets must be distinct")
		}
		seen[p] = true
	}
	// Open each parent before invoking external commands. These handles keep
	// removals in the validated directory even if its pathname is later swapped.
	type target struct {
		path string
		root *os.Root
	}
	var pinned []target
	for _, p := range targets {
		root, e := os.OpenRoot(filepath.Dir(p))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		defer root.Close()
		info, e := root.Lstat(filepath.Base(p))
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if e == nil && !info.Mode().IsRegular() {
			return errors.New("uninstall target changed to a nonregular file")
		}
		pinned = append(pinned, target{p, root})
	}
	if o.Run == nil {
		o.Run = func(ctx context.Context, name string, args ...string) error {
			return exec.CommandContext(ctx, name, args...).Run()
		}
	}
	for _, action := range []string{"stop", "disable"} {
		if e := o.Run(ctx, "systemctl", action, o.ServiceName); e != nil {
			return fmt.Errorf("%s node service: %w", action, e)
		}
	}
	var errs []error
	for _, target := range pinned {
		p, root := target.path, target.root
		var e error
		info, se := root.Lstat(filepath.Base(p))
		if se == nil && !info.Mode().IsRegular() {
			e = errors.New("uninstall target changed to a nonregular file")
		} else if se != nil && !os.IsNotExist(se) {
			e = se
		} else {
			e = root.Remove(filepath.Base(p))
			if os.IsNotExist(e) {
				e = nil
			}
		}
		if e != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, e))
		}
	}
	if e := o.Run(ctx, "systemctl", "daemon-reload"); e != nil {
		errs = append(errs, fmt.Errorf("reload systemd after uninstall: %w", e))
	}
	return errors.Join(errs...)
}
func uninstallTarget(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return errors.New("uninstall requires explicit absolute file paths")
	}
	path = filepath.Clean(path)
	for p := path; ; p = filepath.Dir(p) {
		info, e := os.Lstat(p)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if e == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return errors.New("uninstall target contains a symlink")
			}
			if p == path && !info.Mode().IsRegular() {
				return errors.New("uninstall target must be a regular file")
			}
			if p != path && !info.IsDir() {
				return errors.New("uninstall parent must be a directory")
			}
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}
