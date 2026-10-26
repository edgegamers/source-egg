package addons

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/edgegamers/source-egg/internal/fsx"
)

type Cache struct {
	Refresh  time.Duration
	Dir      string
	HTTP     *http.Client
	Resolver Resolver
	AllowURL func(string) bool
	mu       sync.Mutex
}

func NewCache(dir string, client *http.Client) *Cache {
	if client == nil {
		client = HTTPClient()
	}
	return &Cache{Dir: dir, HTTP: client, Resolver: Resolver{HTTP: client}, AllowURL: OfficialURL}
}

type metadata struct {
	At      time.Time `json:"at"`
	Package Package   `json:"package"`
}

func (c *Cache) Resolve(ctx context.Context, name, pin string) (Package, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if name != "metamod" && name != "sourcemod" {
		return Package{}, errors.New("unsupported framework")
	}
	if e := os.MkdirAll(c.Dir, 0755); e != nil {
		return Package{}, e
	}
	root, e := os.OpenRoot(c.Dir)
	if e != nil {
		return Package{}, e
	}
	defer root.Close()
	sum := sha256.Sum256([]byte(name + ":" + pin))
	path := "metadata/" + hex.EncodeToString(sum[:]) + ".json"
	var old metadata
	have := fsx.ReadJSON(root, path, &old) == nil
	refresh := c.Refresh
	if refresh <= 0 {
		refresh = time.Hour
	}
	if have && time.Since(old.At) < refresh {
		return old.Package, nil
	}
	pkg, e := c.Resolver.Resolve(ctx, name, pin)
	if e != nil {
		if have {
			old.Package.Stale = true
			return old.Package, nil
		}
		return Package{}, e
	}
	if e = fsx.JSON(root, path, metadata{time.Now(), pkg}, 0644); e != nil {
		return Package{}, e
	}
	return pkg, nil
}
func (c *Cache) Fetch(ctx context.Context, p Package) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p.Name != "metamod" && p.Name != "sourcemod" {
		return "", errors.New("unsupported framework")
	}
	if c.AllowURL == nil || !c.AllowURL(p.URL) {
		return "", errors.New("unapproved asset origin")
	}
	key := sha256.Sum256([]byte(p.Name + "\n" + p.Version + "\n" + p.URL))
	dir := filepath.Join(c.Dir, "archives", hex.EncodeToString(key[:]))
	if e := os.MkdirAll(dir, 0755); e != nil {
		return "", e
	}
	dest := filepath.Join(dir, "package.tar.gz")
	checksum := dest + ".sha256"
	if sum, e := os.ReadFile(checksum); e == nil {
		if actual, e := digest(dest); e == nil && actual == strings.TrimSpace(string(sum)) && (p.Digest == "" || p.Digest == "sha256:"+actual) {
			if e = c.recordPackage(dir, p); e != nil {
				return "", e
			}
			now := time.Now()
			if e = os.Chtimes(dest, now, now); e != nil {
				return "", e
			}
			return dest, nil
		}
	}
	req, e := http.NewRequestWithContext(ctx, "GET", p.URL, nil)
	if e != nil {
		return "", e
	}
	resp, e := c.HTTP.Do(req)
	if e != nil {
		return "", e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("asset download HTTP %d", resp.StatusCode)
	}
	tmp := dest + ".part-" + fsx.Token()
	defer os.Remove(tmp)
	f, e := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if e != nil {
		return "", e
	}
	n, e := io.Copy(f, io.LimitReader(resp.Body, (256<<20)+1))
	ce := f.Close()
	if e != nil {
		return "", e
	}
	if ce != nil {
		return "", ce
	}
	if n > 256<<20 {
		return "", errors.New("framework archive exceeds limit")
	}
	sum, e := digest(tmp)
	if e != nil {
		return "", e
	}
	if p.Digest != "" && p.Digest != "sha256:"+sum {
		return "", errors.New("upstream asset checksum mismatch")
	}
	check, e := os.MkdirTemp(dir, "check-")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(check)
	if e = Extract(tmp, check); e != nil {
		return "", e
	}
	if e = os.Rename(tmp, dest); e != nil {
		return "", e
	}
	if e = os.WriteFile(checksum, []byte(sum), 0644); e != nil {
		return "", e
	}
	if e = c.recordPackage(dir, p); e != nil {
		return "", e
	}
	return dest, nil
}
func digest(path string) (string, error) {
	f, e := os.Open(path)
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
