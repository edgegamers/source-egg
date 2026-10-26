package content

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

type DeployOptions struct {
	Method string
	// Host delivery preserves existing ownership and gives newly copied files
	// to the volume owner. Shared hardlink inodes never change ownership.
	PreserveOwnership bool
	// ServerUID is the host UID of the container process, when host delivery
	// can establish it. It may differ from the volume's filesystem owner.
	ServerUID *int
}

func ValidateMethod(method string) error {
	if method != "symlink" && method != "hardlink" && method != "copy" {
		return errors.New("push_method must be symlink, hardlink or copy")
	}
	return nil
}

func seedOnly(rel, gameDir string) bool {
	return strings.HasPrefix(rel, gameDir+"/cfg/") || strings.HasPrefix(rel, gameDir+"/addons/") || rel == gameDir+"/gameinfo.txt"
}
func skipped(rel string) bool {
	return rel == "steamapps" || strings.HasPrefix(rel, "steamapps/") || rel == "Steam" || strings.HasPrefix(rel, "Steam/") || rel == ".source" || strings.HasPrefix(rel, ".source/")
}

// Deploy must run before the game process starts, with a fixed read-only store mount.
func Deploy(g Generation, volume, mountedRoot string) error {
	return DeployWithOptions(g, volume, mountedRoot, DeployOptions{Method: "symlink"})
}

func DeployWithOptions(g Generation, volume, mountedRoot string, options DeployOptions) error {
	if e := ValidateMethod(options.Method); e != nil {
		return e
	}
	if e := g.Identity.Validate(); e != nil {
		return e
	}
	if !filepath.IsLocal(g.Key) || filepath.Clean(g.Key) != g.Key || strings.Count(g.Key, "/") != 4 || !strings.HasPrefix(g.Key, "generations/"+g.Identity.Key()+"/") {
		return errors.New("invalid generation path")
	}
	if !filepath.IsAbs(mountedRoot) {
		return errors.New("shared mount must be absolute")
	}
	p, _ := game.Lookup(g.Identity.Game)
	if _, e := InstalledBuild(g.Path, g.Identity); e != nil {
		return e
	}
	root, e := os.OpenRoot(volume)
	if e != nil {
		return e
	}
	defer root.Close()
	var owner *fileOwner
	if options.PreserveOwnership {
		info, err := root.Stat(".")
		if err != nil {
			return err
		}
		owner, err = ownership(info)
		if err != nil {
			return err
		}
	}
	if e = deploymentDirs(root, ".source", owner); e != nil {
		return e
	}
	// Create a missing identity with the server's ownership. Existing receipts
	// still go through CheckIdentity's strict no-symlink reader and game check.
	if _, err := root.Lstat(".source/identity.json"); os.IsNotExist(err) {
		data, err := json.Marshal(g.Identity)
		if err != nil {
			return err
		}
		if err = writeDeployment(root, ".source/identity.json", bytes.NewReader(data), 0600, owner); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if e = game.CheckIdentity(volume, g.Identity); e != nil {
		return e
	}
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
			return deploymentDirs(root, rel, owner)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("generation symlink refused")
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return errors.New("generation contains a non-regular file")
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
			if e = deploymentDirs(root, filepath.Dir(rel), owner); e != nil {
				return e
			}
			switch options.Method {
			case "symlink":
				tmp := rel + ".link-" + fsx.Token()
				if e = root.Symlink(filepath.Join(mountedRoot, g.Key, rel), tmp); e != nil {
					return e
				}
				defer root.Remove(tmp)
				return root.Rename(tmp, rel)
			case "hardlink":
				if owner != nil || options.ServerUID != nil {
					sharedOwner, err := ownership(info)
					if err != nil {
						return err
					}
					serverUID := 0
					if owner != nil {
						serverUID = owner.uid
					}
					if options.ServerUID != nil {
						serverUID = *options.ServerUID
					}
					if serverUID <= 0 || sharedOwner.uid == serverUID || info.Mode().Perm()&0022 != 0 {
						return fmt.Errorf("hardlink %s: shared inode must be owned by a different user and not writable by the server; choose copy or symlink", rel)
					}
				}
				return linkDeployment(root, path, rel)
			}
		}
		in, e := os.Open(path)
		if e != nil {
			return e
		}
		defer in.Close()
		return writeDeployment(root, rel, in, info.Mode().Perm(), owner)
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
	data, e := json.MarshalIndent(next, "", "  ")
	if e != nil {
		return e
	}
	return writeDeployment(root, ".source/managed.json", bytes.NewReader(append(data, '\n')), 0600, owner)
}

// All replacements use exclusive temporary files and rename, so a prior VPK
// link (or a malicious destination symlink) can never overwrite its target.
func writeDeployment(root *os.Root, name string, in io.Reader, mode fs.FileMode, owner *fileOwner) error {
	if e := deploymentDirs(root, filepath.Dir(name), owner); e != nil {
		return e
	}
	if owner != nil {
		if info, err := root.Lstat(name); err == nil {
			var e error
			owner, e = ownership(info)
			if e != nil {
				return e
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	tmp := name + ".tmp-" + fsx.Token()
	out, e := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if e != nil {
		return e
	}
	defer root.Remove(tmp)
	defer out.Close()
	if _, e = io.Copy(out, in); e != nil {
		return e
	}
	if owner != nil {
		if e = out.Chown(owner.uid, owner.gid); e != nil {
			return e
		}
	}
	if e = out.Sync(); e != nil {
		return e
	}
	if e = out.Close(); e != nil {
		return e
	}
	return root.Rename(tmp, name)
}

func deploymentDirs(root *os.Root, name string, owner *fileOwner) error {
	if name == "." {
		return nil
	}
	if !filepath.IsLocal(name) {
		return errors.New("non-local deployment directory")
	}
	if info, err := root.Lstat(name); err == nil {
		if !info.IsDir() {
			return errors.New("deployment directory is not a real directory")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if e := deploymentDirs(root, filepath.Dir(name), owner); e != nil {
		return e
	}
	if e := root.Mkdir(name, 0755); e != nil {
		return e
	}
	if owner == nil {
		return nil
	}
	dir, e := root.Open(name)
	if e != nil {
		return e
	}
	defer dir.Close()
	info, e := dir.Stat()
	if e != nil {
		return e
	}
	if !info.IsDir() {
		return errors.New("deployment directory changed")
	}
	return dir.Chown(owner.uid, owner.gid)
}
