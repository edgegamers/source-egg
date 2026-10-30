package addons

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/edgegamers/source-egg/internal/fsx"
)

func Extract(archive, dest string) error {
	if e := os.MkdirAll(dest, 0700); e != nil {
		return e
	}
	root, e := os.OpenRoot(dest)
	if e != nil {
		return e
	}
	defer root.Close()
	f, e := os.Open(archive)
	if e != nil {
		return e
	}
	defer f.Close()
	gz, e := gzip.NewReader(f)
	if e != nil {
		return e
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	count := 0
	for {
		h, e := tr.Next()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		name := strings.TrimPrefix(h.Name, "./")
		name = strings.TrimSuffix(name, "/")
		if name == "" || name == "." {
			continue
		}
		if !filepath.IsLocal(name) || strings.Contains(name, "\\") {
			return errors.New("unsafe archive path")
		}
		count++
		total += h.Size
		if count > 100000 || h.Size < 0 || total > 1<<30 {
			return errors.New("unpacked archive exceeds limit")
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if e = root.MkdirAll(name, 0755); e != nil {
				return e
			}
		case tar.TypeReg, tar.TypeRegA:
			if e = root.MkdirAll(filepath.Dir(name), 0755); e != nil {
				return e
			}
			out, e := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fs.FileMode(h.Mode)&0755|0600)
			if e != nil {
				return e
			}
			_, e = io.CopyN(out, tr, h.Size)
			ce := out.Close()
			if e != nil {
				return e
			}
			if ce != nil {
				return ce
			}
		default:
			return errors.New("archive links and special files are not permitted")
		}
	}
}

type Receipt struct {
	Version string            `json:"version"`
	Files   []string          `json:"files"`
	Stock   map[string]string `json:"stock,omitempty"`
}

func private(name string) bool {
	return strings.HasPrefix(name, "cfg/") || strings.HasPrefix(name, "addons/sourcemod/configs/") || strings.HasPrefix(name, "addons/sourcemod/data/") || strings.HasPrefix(name, "addons/sourcemod/logs/") || name == "addons/metamod.vdf"
}
func stockFile(name string) bool {
	return strings.HasPrefix(name, "addons/sourcemod/plugins/") || strings.HasPrefix(name, "addons/sourcemod/extensions/")
}
func rootDigest(root *os.Root, path string) (string, error) {
	st, e := root.Lstat(path)
	if e != nil {
		return "", e
	}
	if !st.Mode().IsRegular() {
		return "", errors.New("framework file must be a regular file")
	}
	f, e := root.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Deploy operates only before the game starts. Seed private/default files once.
func Deploy(archive, volume, gameDir, name, version string, pluginOptions ...map[string]string) error {
	if name != "metamod" && name != "sourcemod" {
		return errors.New("unsupported framework")
	}
	if !filepath.IsLocal(gameDir) || strings.ContainsAny(gameDir, "/\\") {
		return errors.New("invalid game directory")
	}
	var policies map[string]string
	if len(pluginOptions) > 0 && name == "sourcemod" {
		policies = pluginOptions[0]
	}
	if e := ValidatePluginOptions(policies); e != nil {
		return e
	}
	// Wings limits /tmp to 100 MiB by default. Stage on the server volume,
	// like the S2Script egg, so framework size is bounded by disk space.
	temp, e := os.MkdirTemp(volume, ".source-framework-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(temp)
	if e = Extract(archive, temp); e != nil {
		return e
	}
	root, e := os.OpenRoot(volume)
	if e != nil {
		return e
	}
	defer root.Close()
	var old Receipt
	receiptPath := ".source/framework-" + name + ".json"
	e = fsx.ReadJSON(root, receiptPath, &old)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	next := Receipt{Version: version, Stock: map[string]string{}}
	present := map[string]bool{}
	e = filepath.WalkDir(temp, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(temp, path)
		if !strings.HasPrefix(rel, "addons/") && !strings.HasPrefix(rel, "cfg/") {
			return fmt.Errorf("unexpected framework member %s", rel)
		}
		target := filepath.Join(gameDir, rel)
		present[target] = true
		if private(rel) {
			if _, e = root.Lstat(target); e == nil {
				return nil
			} else if !os.IsNotExist(e) {
				return e
			}
		} else if stockFile(rel) {
			canonical := target
			previous, owned := old.Stock[target]
			policy := ""
			if alternate := alternatePluginPath(target); alternate != "" {
				if !owned {
					previous, owned = old.Stock[alternate]
				}
				policy = policies[strings.TrimSuffix(filepath.Base(target), ".smx")]
				target, e = pluginTarget(root, target, previous, owned, policy)
				if e != nil {
					return e
				}
				present[target] = true
			}
			current, err := rootDigest(root, target)
			explicit := policy == "0" || policy == "1"
			if owned && ((!explicit && os.IsNotExist(err)) || (err == nil && current != previous)) {
				next.Stock[canonical] = previous // Keep manual edits and disabled/deleted defaults.
				return nil
			}
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			if !owned && err == nil {
				return nil
			} // A pre-existing custom extension/plugin.
			sum, err := digest(path)
			if err != nil {
				return err
			}
			next.Stock[canonical] = sum
		} else {
			next.Files = append(next.Files, target)
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		return fsx.Copy(root, target, path, info.Mode().Perm())
	})
	if e != nil {
		return e
	}
	for _, oldPath := range old.Files {
		if !filepath.IsLocal(oldPath) || !strings.HasPrefix(oldPath, gameDir+"/") {
			return errors.New("invalid framework receipt")
		}
		rel := strings.TrimPrefix(oldPath, gameDir+"/")
		if !private(rel) && !present[oldPath] {
			if e = root.Remove(oldPath); e != nil && !os.IsNotExist(e) {
				return e
			}
		}
	}
	for path, sum := range old.Stock {
		if !filepath.IsLocal(path) || !strings.HasPrefix(path, gameDir+"/") || !stockFile(strings.TrimPrefix(path, gameDir+"/")) {
			return errors.New("invalid stock-file receipt")
		}
		candidates := []string{path}
		if alternate := alternatePluginPath(path); alternate != "" {
			candidates = append(candidates, alternate)
		}
		for _, candidate := range candidates {
			if present[candidate] {
				continue
			}
			if err := stockPathNoLinks(root, candidate); err != nil {
				return err
			}
			if current, err := rootDigest(root, candidate); err == nil && current == sum {
				if err = root.Remove(candidate); err != nil {
					return err
				}
			}
		}
	}
	return fsx.JSON(root, receiptPath, next, 0600)
}
