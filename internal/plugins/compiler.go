package plugins

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/edgegamers/source-egg/internal/addons"
)

type compilerReceipt struct {
	Tag    string `json:"tag"`
	SHA256 string `json:"sha256"`
}

// Pin the private cache root, then use the installer's no-follow filesystem
// operations for every cached file. The broader framework cache operates only
// in a fresh work directory, never over server-supplied cache descendants.
func openCompilerCache(dir string) (*installFS, error) {
	if dir == "" {
		return nil, errors.New("private compiler cache is unavailable")
	}
	parent, name, err := acquisitionParent(dir)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	if err := parent.Mkdir(name, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("toolchain cache contains a symlink")
	}
	if !info.IsDir() {
		return nil, errors.New("toolchain cache is not a directory")
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	base, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	return &installFS{base: base}, nil
}

func (b SourceBuilder) compilerDirectory(ctx context.Context, tag, work string) (string, error) {
	if !compilerTag.MatchString(tag) {
		return "", errors.New("compiler requires an exact SourceMod tag")
	}
	cache, err := openCompilerCache(b.CacheDir)
	if err != nil {
		return "", err
	}
	defer cache.Close()
	name := "sourcemod-" + tag
	archive := filepath.Join(work, "compiler.tar.gz")
	var receipt compilerReceipt
	err = cache.readJSON(name+".json", &receipt)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	cached := false
	if err == nil {
		if receipt.Tag != tag || !acquisitionDigest(receipt.SHA256) {
			return "", errors.New("invalid private compiler receipt")
		}
		input, err := cache.openRegular(name+".tar.gz", true)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if err == nil {
			output, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				input.Close()
				return "", err
			}
			hash := sha256.New()
			n, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(input, maxPluginDownload+1))
			input.Close()
			closeErr := output.Close()
			if copyErr != nil || closeErr != nil {
				return "", errors.New("could not read private compiler archive")
			}
			cached = n <= maxPluginDownload && hex.EncodeToString(hash.Sum(nil)) == receipt.SHA256
			if !cached {
				if err := os.Remove(archive); err != nil {
					return "", err
				}
			}
		}
	}
	if !cached {
		download := addons.NewCache(filepath.Join(work, "compiler-download"), nil)
		pkg, err := download.Resolve(ctx, "sourcemod", tag)
		if err != nil {
			return "", err
		}
		archive, err = download.Fetch(ctx, pkg)
		if err != nil {
			return "", err
		}
		input, err := os.Open(archive)
		if err != nil {
			return "", err
		}
		defer input.Close()
		hash := sha256.New()
		n, err := io.Copy(hash, io.LimitReader(input, maxPluginDownload+1))
		if err != nil || n > maxPluginDownload {
			return "", errors.New("invalid downloaded compiler archive")
		}
		receipt = compilerReceipt{Tag: tag, SHA256: hex.EncodeToString(hash.Sum(nil))}
		if _, err := input.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		if err := cache.atomic(name+".tar.gz", input, receipt.SHA256, 0600); err != nil {
			return "", err
		}
		data, _ := json.Marshal(receipt)
		if err := cache.atomic(name+".json", bytes.NewReader(data), "", 0600); err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir := filepath.Join(work, "toolchain")
	if err := addons.Extract(archive, dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, "addons/sourcemod/scripting"), nil
}
