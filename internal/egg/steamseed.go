package egg

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/edgegamers/source-egg/internal/fsx"
)

// SeedSteamCMD copies the image seed into the writable server volume once.
// Wings makes /opt read-only and can use any configured container UID.
func SeedSteamCMD(seed, volume string) error {
	root, err := os.OpenRoot(volume)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err = root.Stat(".source/steamcmd/steamcmd.sh"); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	tmp := ".source/steamcmd-seed-" + fsx.Token()
	if err = root.MkdirAll(tmp, 0700); err != nil {
		return err
	}
	defer root.RemoveAll(tmp)
	if err = filepath.WalkDir(seed, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(seed, path)
		if rel == "." {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("unexpected SteamCMD seed symlink")
		}
		if d.IsDir() {
			return root.MkdirAll(filepath.Join(tmp, rel), 0755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := fs.FileMode(0644)
		if info.Mode()&0111 != 0 {
			mode = 0755
		}
		return fsx.Copy(root, filepath.Join(tmp, rel), path, mode)
	}); err != nil {
		return err
	}
	return root.Rename(tmp, ".source/steamcmd")
}
