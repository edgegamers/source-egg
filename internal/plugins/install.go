package plugins

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
)

// Installer deploys packages only while the server is stopped. Its caller must
// serialize calls; the journal is crash recovery, not a concurrent-writer lock.
type Installer struct {
	Volume, GameDir string
	Preserve        map[string][]string
}

// ErrRecovery means an interrupted deployment cannot yet be restored safely.
// Starting a game process after this error could load a partial installation.
var ErrRecovery = errors.New("plugin recovery incomplete")

const installedPath = ".source/plugins/installed.json"
const journalPath = ".source/plugins/journal.json"

type installationReceipt struct {
	Version int                     `json:"version"`
	Plugins map[string]Installation `json:"plugins"`
}

func validDigest(s string) bool {
	if len(s) != 64 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
func validateSnapshot(s Snapshot) error {
	if !validDigest(s.ID) || s.Directory != ".source/plugins/packages/"+s.ID || s.Version == "" || s.Recipe == "" || len(s.Files) == 0 || len(s.Files) > 100000 {
		return errors.New("invalid plugin snapshot")
	}
	for name, file := range s.Files {
		if !ValidTarget(name) || !strings.Contains(name, "/") || !validDigest(file.SHA256) {
			return fmt.Errorf("invalid snapshot file %q", name)
		}
	}
	return nil
}
func (i Installer) open() (*installFS, error) {
	if !identifier.MatchString(i.GameDir) {
		return nil, errors.New("invalid game directory")
	}
	return openInstallFS(i.Volume)
}
func (i Installer) Installed() (map[string]Installation, error) {
	f, err := i.open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err = f.noJournal(); err != nil {
		return nil, err
	}
	return loadInstallations(f)
}
func loadInstallations(f *installFS) (map[string]Installation, error) {
	var receipt installationReceipt
	err := f.readJSON(installedPath, &receipt)
	if os.IsNotExist(err) {
		return map[string]Installation{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read plugin receipts: %w", err)
	}
	if receipt.Version != 1 || receipt.Plugins == nil {
		return nil, errors.New("invalid plugin receipts")
	}
	owned := map[string]string{}
	for id, installation := range receipt.Plugins {
		if !identifier.MatchString(id) || len(installation.Previous) > 2 {
			return nil, errors.New("invalid plugin installation receipt")
		}
		for _, snapshot := range append([]Snapshot{installation.Current}, installation.Previous...) {
			if err := validateSnapshot(snapshot); err != nil {
				return nil, fmt.Errorf("%s receipt: %w", id, err)
			}
		}
		for name := range installation.Current.Files {
			if other := owned[name]; other != "" {
				return nil, fmt.Errorf("receipts assign %s to both %s and %s", name, other, id)
			}
			owned[name] = id
		}
	}
	return receipt.Plugins, nil
}

func (i Installer) owners(f *installFS, installed map[string]Installation, id string) (map[string]string, error) {
	owners := map[string]string{}
	for owner, installation := range installed {
		if owner != id {
			for name := range installation.Current.Files {
				owners[name] = owner
			}
		}
	}
	for _, framework := range []string{"metamod", "sourcemod"} {
		var receipt struct {
			Version string            `json:"version"`
			Files   []string          `json:"files"`
			Stock   map[string]string `json:"stock,omitempty"`
		}
		err := f.readJSON(".source/framework-"+framework+".json", &receipt)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s ownership: %w", framework, err)
		}
		if receipt.Version == "" {
			return nil, fmt.Errorf("invalid %s framework receipt", framework)
		}
		names := append([]string(nil), receipt.Files...)
		for name, digest := range receipt.Stock {
			if !validDigest(digest) {
				return nil, fmt.Errorf("invalid %s framework digest", framework)
			}
			names = append(names, name)
			if alternate := alternateFrameworkPlugin(name); alternate != "" {
				names = append(names, alternate)
			}
		}
		for _, name := range names {
			rel, ok := strings.CutPrefix(name, i.GameDir+"/")
			if !ok || !ValidTarget(rel) || !strings.Contains(rel, "/") {
				return nil, fmt.Errorf("invalid %s framework file %q", framework, name)
			}
			owners[rel] = "framework " + framework
		}
	}
	return owners, nil
}
func alternateFrameworkPlugin(name string) string {
	if !strings.HasSuffix(name, ".smx") {
		return ""
	}
	dir := path.Dir(name)
	if strings.HasSuffix(dir, "/addons/sourcemod/plugins") {
		return dir + "/disabled/" + path.Base(name)
	}
	if strings.HasSuffix(dir, "/addons/sourcemod/plugins/disabled") {
		return path.Dir(dir) + "/" + path.Base(name)
	}
	return ""
}

func (i Installer) staged(f *installFS, next Snapshot) error {
	if err := validateSnapshot(next); err != nil {
		return err
	}
	// Validate the whole package before creating a journal or touching game files.
	for _, name := range sortedSnapshotFiles(next.Files) {
		digest, _, err := f.digest(next.Directory+"/"+name, true)
		if err != nil {
			return fmt.Errorf("staged %s: %w", name, err)
		}
		if digest != next.Files[name].SHA256 {
			return fmt.Errorf("staged %s does not match approved SHA256", name)
		}
	}
	return nil
}
func sortedSnapshotFiles(files map[string]File) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Preview returns a stable path-sorted list. Modified managed files require an
// explicit replace choice; unowned files require adoption of a matching package.
func (i Installer) Preview(id string, next *Snapshot, replace bool) ([]Change, error) {
	f, err := i.open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err = f.noJournal(); err != nil {
		return nil, err
	}
	installed, err := loadInstallations(f)
	if err != nil {
		return nil, err
	}
	changes, _, err := i.plan(f, installed, id, next, replace, false)
	return changes, err
}

func (i Installer) plan(f *installFS, installed map[string]Installation, id string, next *Snapshot, replace, adopt bool) ([]Change, []fileMutation, error) {
	if !identifier.MatchString(id) {
		return nil, nil, errors.New("invalid plugin id")
	}
	old, exists := installed[id]
	if adopt && exists {
		return nil, nil, errors.New("plugin is already managed; use update")
	}
	if next == nil && !exists {
		return nil, nil, fmt.Errorf("plugin %s is not installed", id)
	}
	if next != nil {
		if err := i.staged(f, *next); err != nil {
			return nil, nil, err
		}
	}
	owners, err := i.owners(f, installed, id)
	if err != nil {
		return nil, nil, err
	}
	names := map[string]File{}
	for name, file := range old.Current.Files {
		names[name] = file
	}
	if next != nil {
		for name, file := range next.Files {
			names[name] = file
		}
	}
	var changes []Change
	var mutations []fileMutation
	for _, name := range sortedSnapshotFiles(names) {
		before, owned := old.Current.Files[name]
		after, wanted := File{}, false
		if next != nil {
			after, wanted = next.Files[name]
		}
		if owner := owners[name]; owner != "" {
			return changes, nil, fmt.Errorf("%s is owned by %s", name, owner)
		}
		destination := i.GameDir + "/" + name
		digest, _, err := f.digest(destination, false)
		present := err == nil
		if err != nil && !os.IsNotExist(err) {
			return changes, nil, fmt.Errorf("%s: %w", name, err)
		}
		seed := Protected(name, i.Preserve[id]) || (owned && before.Seed) || (wanted && after.Seed)
		action := "preserve"
		switch {
		case adopt:
			if !seed && (!present || digest != after.SHA256) {
				return changes, nil, fmt.Errorf("cannot adopt %s: every managed file must exist and match the staged package", name)
			}
			if !seed {
				action = "adopt"
			}
		case seed:
			if wanted && !present {
				action = "seed"
				mutations = append(mutations, fileMutation{Path: destination, BeforeSHA: digest, Source: next.Directory + "/" + name, SHA256: after.SHA256})
			}
		case !wanted:
			if present && digest == before.SHA256 {
				action = "remove"
				mutations = append(mutations, fileMutation{Path: destination, BeforeSHA: digest, Remove: true})
			} else if next != nil && present {
				if !replace {
					return changes, nil, fmt.Errorf("%s has local changes; keep the current package or explicitly replace", name)
				}
				action = "remove"
				mutations = append(mutations, fileMutation{Path: destination, BeforeSHA: digest, Remove: true})
			}
		case !owned && present:
			return changes, nil, fmt.Errorf("%s is unowned; explicit adoption of a matching package is required", name)
		case owned && (!present || digest != before.SHA256) && !replace:
			return changes, nil, fmt.Errorf("%s has local changes; keep the current package or explicitly replace", name)
		case !present || digest != after.SHA256:
			action = "install"
			if owned {
				action = "update"
			}
			mutations = append(mutations, fileMutation{Path: destination, BeforeSHA: digest, Source: next.Directory + "/" + name, SHA256: after.SHA256})
		default:
			action = "keep"
		}
		changes = append(changes, Change{Path: name, Action: action})
	}
	return changes, mutations, nil
}

func normalizeSnapshot(next Snapshot, old Installation, preserve []string) Snapshot {
	result := next
	result.Files = make(map[string]File, len(next.Files))
	for name, file := range next.Files {
		file.Seed = file.Seed || Protected(name, preserve) || old.Current.Files[name].Seed
		result.Files[name] = file
	}
	return result
}
func nextInstallation(next Snapshot, old Installation, preserve []string) Installation {
	result := Installation{Current: normalizeSnapshot(next, old, preserve)}
	seen := map[string]bool{next.ID: true}
	for _, snapshot := range append([]Snapshot{old.Current}, old.Previous...) {
		if snapshot.ID != "" && !seen[snapshot.ID] {
			result.Previous = append(result.Previous, snapshot)
			seen[snapshot.ID] = true
			if len(result.Previous) == 2 {
				break
			}
		}
	}
	return result
}
func (i Installer) Apply(id string, next Snapshot, replace bool) error {
	return i.change(id, &next, replace, false)
}
func (i Installer) Adopt(id string, next Snapshot) error { return i.change(id, &next, false, true) }
func (i Installer) Uninstall(id string) error            { return i.change(id, nil, false, false) }
func (i Installer) change(id string, next *Snapshot, replace, adopt bool) error {
	if err := i.Recover(); err != nil {
		return err
	}
	f, err := i.open()
	if err != nil {
		return err
	}
	defer f.Close()
	installed, err := loadInstallations(f)
	if err != nil {
		return err
	}
	_, mutations, err := i.plan(f, installed, id, next, replace, adopt)
	if err != nil {
		return err
	}
	if next == nil {
		delete(installed, id)
	} else {
		installed[id] = nextInstallation(*next, installed[id], i.Preserve[id])
	}
	return i.transact(f, mutations, installationReceipt{Version: 1, Plugins: installed})
}

// sourceReader checks a source again during the atomic copy, so a changed staged
// package cannot be substituted between preflight and a destination rename.
func sourceReader(f *installFS, source string) (*os.File, os.FileMode, error) {
	in, err := f.openRegular(source, true)
	if err != nil {
		return nil, 0, err
	}
	st, err := in.Stat()
	if err != nil {
		in.Close()
		return nil, 0, err
	}
	return in, st.Mode().Perm(), nil
}
func copyMutation(f *installFS, m fileMutation) error {
	if m.Remove {
		return f.remove(m.Path)
	}
	source, mode, err := sourceReader(f, m.Source)
	if err != nil {
		return err
	}
	defer source.Close()
	return f.atomic(m.Path, io.LimitReader(source, maxInstallFileBytes+1), m.SHA256, mode)
}
