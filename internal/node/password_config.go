package node

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// passwordDirectory keeps legacy storage beneath private node state unless an
// operator explicitly supplies another private directory.
func (c Config) passwordDirectory() string {
	if c.PasswordDir != "" {
		return filepath.Clean(c.PasswordDir)
	}
	return filepath.Join(c.StateDir, "passwords")
}

// validatePasswordDirectory runs during config loading and daemon startup,
// before MkdirAll or Chmod can follow an operator-supplied alias. Missing leaf
// directories are allowed; every existing private ancestor must be a directory
// and must not be a symlink. Public roots are resolved independently so aliases
// on either side cannot hide the private/public relationship.
func (c Config) validatePasswordDirectory() error {
	private := c.passwordDirectory()
	for current := private; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return errors.New("password directory includes a symlink; use its real private path")
			}
			if !info.IsDir() {
				return errors.New("password directory ancestor is not a directory")
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("check password directory: %w", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	for _, public := range []string{c.SharedDir, c.CacheDir} {
		realPublic, err := resolveExistingDirectory(public)
		if err != nil {
			return fmt.Errorf("resolve public password boundary: %w", err)
		}
		relative, err := filepath.Rel(realPublic, private)
		if err != nil {
			return err
		}
		if relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("password storage must be outside public shared/cache mounts")
		}
	}
	return nil
}

// resolveExistingDirectory resolves existing aliases without creating missing
// descendants. Split preserves alias/.. ordering until EvalSymlinks resolves it.
func resolveExistingDirectory(directory string) (string, error) {
	current := strings.TrimRight(directory, string(filepath.Separator))
	if current == "" {
		current = string(filepath.Separator)
	}
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			info, statErr := os.Stat(resolved)
			if statErr != nil {
				return "", statErr
			}
			if !info.IsDir() {
				return "", errors.New("public mount ancestor is not a directory")
			}
			return filepath.Join(append([]string{resolved}, suffix...)...), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		// An existing dangling symlink is not a prospective plain directory.
		if _, statErr := os.Lstat(current); statErr == nil {
			return "", err
		} else if !os.IsNotExist(statErr) {
			return "", statErr
		}
		parent, name := filepath.Split(current)
		if name == "" || parent == "" {
			return "", err
		}
		suffix = append([]string{name}, suffix...)
		current = strings.TrimRight(parent, string(filepath.Separator))
		if current == "" {
			current = string(filepath.Separator)
		}
	}
}
