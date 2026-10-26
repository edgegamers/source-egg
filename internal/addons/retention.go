package addons

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/edgegamers/source-egg/internal/fsx"
)

func (c *Cache) recordPackage(dir string, p Package) error {
	root, e := os.OpenRoot(dir)
	if e != nil {
		return e
	}
	defer root.Close()
	return fsx.JSON(root, "package.json", p, 0644)
}

// Prune removes aged, known package archives only. Missing legacy metadata is
// retained until the archive has been requested again and its identity recorded.
func (c *Cache) Prune(keep time.Duration, protected map[string]bool) (int, error) {
	if keep <= 0 {
		return 0, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	root, e := os.OpenRoot(filepath.Join(c.Dir, "archives"))
	if os.IsNotExist(e) {
		return 0, nil
	}
	if e != nil {
		return 0, e
	}
	defer root.Close()
	dir, e := root.Open(".")
	if e != nil {
		return 0, e
	}
	defer dir.Close()
	removed := 0
	var errs []error
	cutoff := time.Now().Add(-keep)
	validDirectory := regexp.MustCompile(`^[a-f0-9]{64}$`)
	for {
		entries, e := dir.ReadDir(128)
		for _, entry := range entries {
			if !entry.IsDir() || !validDirectory.MatchString(entry.Name()) {
				continue
			}
			var p Package
			if fsx.ReadJSON(root, entry.Name()+"/package.json", &p) != nil || p.Name != "metamod" && p.Name != "sourcemod" || p.Version == "" || protected[p.Name+":"+p.Version] {
				continue
			}
			info, err := root.Lstat(entry.Name() + "/package.tar.gz")
			if err != nil || !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
				continue
			}
			if err = root.RemoveAll(entry.Name()); err != nil {
				errs = append(errs, err)
			} else {
				removed++
			}
		}
		if e != nil {
			if e != io.EOF {
				errs = append(errs, e)
			}
			break
		}
	}
	return removed, errors.Join(errs...)
}
