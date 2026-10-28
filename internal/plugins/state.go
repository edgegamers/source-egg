package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/edgegamers/source-egg/internal/fsx"
)

const manifestPath = ".source/plugins.json"
const credentialsPath = ".source/plugin-credentials.json"
const statePath = ".source/plugins/state.json"

type pendingAction struct {
	Kind     string   `json:"kind"`
	Snapshot Snapshot `json:"snapshot"`
	Recipe   string   `json:"recipe,omitempty"`
	Replace  bool     `json:"replace,omitempty"`
	Preserve []string `json:"preserve,omitempty"`
}
type managerState struct {
	Version   int                      `json:"version"`
	Available map[string]Snapshot      `json:"available"`
	Pending   map[string]pendingAction `json:"pending"`
	Checked   map[string]time.Time     `json:"checked"`
}

func freshState() managerState {
	return managerState{Version: 1, Available: map[string]Snapshot{}, Pending: map[string]pendingAction{}, Checked: map[string]time.Time{}}
}

// Reject links throughout manager-owned paths, including links within the volume.
func managerPath(root *os.Root, name string, missing bool) error {
	if !cleanRelative(name, false) {
		return errors.New("invalid manager path")
	}
	parts := strings.Split(name, "/")
	for n := range parts {
		st, e := root.Lstat(strings.Join(parts[:n+1], "/"))
		if os.IsNotExist(e) && missing {
			return nil
		}
		if e != nil {
			return e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return errors.New("manager path contains a symbolic link")
		}
		if n < len(parts)-1 && !st.IsDir() {
			return errors.New("manager parent is not a directory")
		}
	}
	return nil
}
func managerRead(root *os.Root, name string, v any, max int64) error {
	if err := managerPath(root, name, false); err != nil {
		return err
	}
	f, e := root.Open(name)
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() || st.Size() > max {
		return fmt.Errorf("%s must be a regular file smaller than %d bytes", name, max)
	}
	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if e != nil {
		return e
	}
	if int64(len(b)) > max {
		return errors.New("JSON file exceeds limit")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(v); e != nil {
		return e
	}
	if e = d.Decode(new(any)); e != io.EOF {
		return errors.New("JSON must contain one object")
	}
	return nil
}
func managerWrite(root *os.Root, name string, v any) error {
	if e := managerPath(root, name, true); e != nil {
		return e
	}
	if e := fsx.JSON(root, name, v, 0600); e != nil {
		return e
	}
	dir, e := root.Open(path.Dir(name))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
func (m *Manager) root() (*os.Root, error) { return os.OpenRoot(m.volume) }
func (m *Manager) reload() error {
	root, e := m.root()
	if e != nil {
		return e
	}
	defer root.Close()
	var manifest Manifest
	if e = managerRead(root, manifestPath, &manifest, 256<<10); e != nil {
		return e
	}
	if e = manifest.Validate(); e != nil {
		return e
	}
	m.manifest = manifest
	m.installer.Preserve = map[string][]string{}
	for _, p := range manifest.Plugins {
		m.installer.Preserve[p.ID] = append([]string(nil), p.Preserve...)
	}
	if m.defaultSource {
		creds := map[string]string{}
		if e = managerRead(root, credentialsPath, &creds, 64<<10); e != nil && !os.IsNotExist(e) {
			return errors.New("cannot read .source/plugin-credentials.json (expected an object of credential names and tokens)")
		}
		for key, value := range creds {
			if !identifier.MatchString(key) || len(value) > 8192 || strings.ContainsAny(value, "\r\n\x00") {
				return errors.New("invalid plugin credential name or token")
			}
		}
		m.source = &HTTPSource{Credentials: creds}
	}
	return nil
}
func (m *Manager) save() error {
	r, e := m.root()
	if e != nil {
		return e
	}
	defer r.Close()
	return managerWrite(r, statePath, m.state)
}
func (m *Manager) writeManifest() error {
	r, e := m.root()
	if e != nil {
		return e
	}
	defer r.Close()
	return managerWrite(r, manifestPath, m.manifest)
}
func validSnapshotReference(s Snapshot) bool {
	if len(s.ID) != 64 || s.Directory != ".source/plugins/packages/"+s.ID || len(s.Files) == 0 || len(s.Files) > 10000 {
		return false
	}
	for _, c := range s.ID {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	for name, f := range s.Files {
		if !ValidTarget(name) || len(f.SHA256) != 64 {
			return false
		}
	}
	return len(s.Version) > 0 && len(s.Version) < 512 && len(s.Recipe) == 64
}
