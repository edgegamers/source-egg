package content

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/edgegamers/source-egg/internal/fsx"
	"github.com/edgegamers/source-egg/internal/game"
)

type Installer interface {
	Install(context.Context, game.Identity, string) error
}
type checker interface {
	RemoteBuild(context.Context, game.Identity) (string, error)
}
type Generation struct {
	Identity game.Identity `json:"identity"`
	Key      string        `json:"key"`
	BuildID  string        `json:"build_id"`
	Path     string        `json:"-"`
}
type Store struct {
	Dir       string
	Installer Installer
	mu        sync.Mutex
}

func (s *Store) Current(id game.Identity) (Generation, error) {
	if e := id.Validate(); e != nil {
		return Generation{}, e
	}
	root, e := os.OpenRoot(s.Dir)
	if e != nil {
		return Generation{}, e
	}
	defer root.Close()
	var g Generation
	if e = fsx.ReadJSON(root, "current/"+id.Key()+".json", &g); e != nil {
		return g, e
	}
	if g.Identity != id || !filepath.IsLocal(g.Key) || !strings.HasPrefix(g.Key, "generations/"+id.Key()+"/") {
		return Generation{}, errors.New("invalid generation receipt")
	}
	g.Path = filepath.Join(s.Dir, g.Key)
	return g, nil
}
func (s *Store) Ensure(ctx context.Context, id game.Identity) (Generation, error) {
	if e := id.Validate(); e != nil {
		return Generation{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Installer == nil {
		return Generation{}, errors.New("no Steam installer")
	}
	if e := os.MkdirAll(s.Dir, 0755); e != nil {
		return Generation{}, e
	}
	previous, haveErr := s.Current(id)
	if check, ok := s.Installer.(checker); ok && haveErr == nil {
		remote, e := check.RemoteBuild(ctx, id)
		if e != nil {
			return Generation{}, e
		}
		if remote == previous.BuildID {
			return previous, nil
		}
	}
	stage, e := os.MkdirTemp(s.Dir, ".staging-")
	if e != nil {
		return Generation{}, e
	}
	defer os.RemoveAll(stage)
	if haveErr == nil {
		if e = clone(previous.Path, stage); e != nil {
			return Generation{}, e
		}
	}
	if e = s.Installer.Install(ctx, id, stage); e != nil {
		return Generation{}, e
	}
	build, e := InstalledBuild(stage, id)
	if e != nil {
		return Generation{}, e
	}
	if haveErr == nil && build == previous.BuildID {
		return previous, nil
	}
	g := Generation{Identity: id, Key: "generations/" + id.Key() + "/" + build + "-" + fsx.Token(), BuildID: build}
	g.Path = filepath.Join(s.Dir, g.Key)
	// Staging is private; normalize published files so container users can read.
	stageRoot, e := os.OpenRoot(stage)
	if e != nil {
		return Generation{}, e
	}
	defer stageRoot.Close()
	if e = filepath.WalkDir(stage, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			rel, _ := filepath.Rel(stage, path)
			f, err := stageRoot.Open(rel)
			if err != nil {
				return err
			}
			defer f.Close()
			info, err := f.Stat()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return errors.New("Steam symlink must resolve to a regular file within the install")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			mode := fs.FileMode(0644)
			if info.Mode()&0111 != 0 {
				mode = 0755
			}
			return fsx.Atomic(stageRoot, rel, data, mode)
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		mode := fs.FileMode(0644)
		if d.IsDir() || info.Mode()&0111 != 0 {
			mode = 0755
		}
		return os.Chmod(path, mode)
	}); e != nil {
		return Generation{}, e
	}
	root, e := os.OpenRoot(s.Dir)
	if e != nil {
		return Generation{}, e
	}
	defer root.Close()
	if e = root.MkdirAll(filepath.Dir(g.Key), 0755); e != nil {
		return Generation{}, e
	}
	if e = os.Rename(stage, g.Path); e != nil {
		return Generation{}, e
	}
	if e = fsx.JSON(root, "current/"+id.Key()+".json", g, 0644); e != nil {
		return Generation{}, e
	}
	return g, nil
}
func clone(src, dst string) error {
	root, e := os.OpenRoot(dst)
	if e != nil {
		return e
	}
	defer root.Close()
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("snapshot symlink not supported")
		}
		if d.IsDir() {
			return root.MkdirAll(rel, 0755)
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		return fsx.Copy(root, rel, path, info.Mode().Perm())
	})
}

// Prune is explicit and receives all persisted active/stopped/pinned references.
func (s *Store) Prune(referenced map[string]bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	currentDir := filepath.Join(s.Dir, "current")
	e := filepath.WalkDir(currentDir, func(path string, d fs.DirEntry, e error) error {
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		root, e := os.OpenRoot(s.Dir)
		if e != nil {
			return e
		}
		defer root.Close()
		rel, _ := filepath.Rel(s.Dir, path)
		var g Generation
		if e = fsx.ReadJSON(root, rel, &g); e != nil {
			return e
		}
		referenced[g.Key] = true
		return nil
	})
	if e != nil {
		return e
	}
	return filepath.WalkDir(filepath.Join(s.Dir, "generations"), func(path string, d fs.DirEntry, e error) error {
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if !d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(s.Dir, path)
		if len(strings.Split(filepath.ToSlash(rel), "/")) != 5 {
			return nil
		}
		if !referenced[filepath.ToSlash(rel)] {
			if e = os.RemoveAll(path); e != nil {
				return e
			}
		}
		return filepath.SkipDir
	})
}
