package operations

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func storagePath(p string) error {
	if !filepath.IsAbs(p) {
		return errors.New("storage path must be absolute")
	}
	clean := filepath.Clean(p)
	slash := filepath.ToSlash(clean)
	for _, system := range []string{"/etc", "/usr", "/bin", "/sbin", "/proc", "/sys", "/dev"} {
		if slash == system || strings.HasPrefix(slash, system+"/") {
			return errors.New("storage path names a system directory")
		}
	}
	for _, base := range []string{"/", "/var", "/var/lib", "/var/tmp", "/tmp", "/home", "/root", "/srv", "/mnt", "/media", "/run", "/opt"} {
		if slash == base {
			return errors.New("storage path must name a dedicated directory")
		}
	}
	if clean == filepath.VolumeName(clean)+string(filepath.Separator) {
		return errors.New("storage path must not name filesystem root")
	}
	return nil
}

// secureRoot walks pinned directory descriptors and rejects every symlink
// ancestor. Existing directories are never chmodded. The identity comparison
// also rejects an ancestor swapped for a link while its descriptor is opened.
func secureRoot(p string, create bool) (*os.Root, error) {
	if e := storagePath(p); e != nil {
		return nil, e
	}
	p = filepath.Clean(p)
	volume := filepath.VolumeName(p)
	root, e := os.OpenRoot(volume + string(filepath.Separator))
	if e != nil {
		return nil, e
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(p, volume), string(filepath.Separator))
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		st, e := root.Lstat(component)
		if errors.Is(e, os.ErrNotExist) && create {
			e = root.Mkdir(component, 0700)
			if e != nil && !errors.Is(e, os.ErrExist) {
				root.Close()
				return nil, e
			}
			st, e = root.Lstat(component)
		}
		if e != nil {
			root.Close()
			return nil, e
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			root.Close()
			return nil, errors.New("storage ancestor is not a real directory")
		}
		next, e := root.OpenRoot(component)
		if e != nil {
			root.Close()
			return nil, e
		}
		current, e := next.Stat(".")
		if e != nil || !os.SameFile(st, current) {
			next.Close()
			root.Close()
			return nil, errors.New("storage ancestor changed while opening")
		}
		root.Close()
		root = next
	}
	return root, nil
}
