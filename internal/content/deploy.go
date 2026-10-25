package content

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/edgegamers/source-egg/internal/fsx"
	"github.com/edgegamers/source-egg/internal/game"
)

type Deployment struct {
	Generation Generation `json:"generation"`
	Files      []string   `json:"files"`
}

func seedOnly(rel, gameDir string) bool {
	return strings.HasPrefix(rel, gameDir+"/cfg/") || strings.HasPrefix(rel, gameDir+"/addons/") || rel == gameDir+"/gameinfo.txt"
}
func skipped(rel string) bool {
	return rel == "steamapps" || strings.HasPrefix(rel, "steamapps/") || rel == "Steam" || strings.HasPrefix(rel, "Steam/") || rel == ".source" || strings.HasPrefix(rel, ".source/")
}

// Deploy must run before the game process starts, with a fixed read-only store mount.
func Deploy(g Generation, volume, mountedRoot string) error {
	if e := g.Identity.Validate(); e != nil {
		return e
	}
	if !filepath.IsLocal(g.Key) || !strings.HasPrefix(g.Key, "generations/"+g.Identity.Key()+"/") {
		return errors.New("invalid generation path")
	}
	if !filepath.IsAbs(mountedRoot) {
		return errors.New("shared mount must be absolute")
	}
	p, _ := game.Lookup(g.Identity.Game)
	if _, e := InstalledBuild(g.Path, g.Identity); e != nil {
		return e
	}
	if e := game.CheckIdentity(volume, g.Identity); e != nil {
		return e
	}
	root, e := os.OpenRoot(volume)
	if e != nil {
		return e
	}
	defer root.Close()
	var old Deployment
	e = fsx.ReadJSON(root, ".source/managed.json", &old)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	next := Deployment{Generation: g}
	present := map[string]bool{}
	e = filepath.WalkDir(g.Path, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(g.Path, path)
		if rel == "." {
			return nil
		}
		if skipped(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return root.MkdirAll(rel, 0755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("generation symlink refused")
		}
		present[rel] = true
		if seedOnly(rel, p.Dir) {
			if _, e = root.Lstat(rel); e == nil {
				return nil
			} else if !os.IsNotExist(e) {
				return e
			}
		} else {
			next.Files = append(next.Files, rel)
		}
		if strings.HasSuffix(rel, ".vpk") && !seedOnly(rel, p.Dir) {
			if e = root.MkdirAll(filepath.Dir(rel), 0755); e != nil {
				return e
			}
			tmp := rel + ".link-" + fsx.Token()
			if e = root.Symlink(filepath.Join(mountedRoot, g.Key, rel), tmp); e != nil {
				return e
			}
			defer root.Remove(tmp)
			return root.Rename(tmp, rel)
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		return fsx.Copy(root, rel, path, info.Mode().Perm())
	})
	if e != nil {
		return e
	}
	for _, rel := range old.Files {
		if !filepath.IsLocal(rel) || skipped(rel) || seedOnly(rel, p.Dir) {
			return errors.New("invalid managed file receipt")
		}
		if !present[rel] {
			if e = root.Remove(rel); e != nil && !os.IsNotExist(e) {
				return e
			}
		}
	}
	return fsx.JSON(root, ".source/managed.json", next, 0600)
}
