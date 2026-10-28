package plugins

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/edgegamers/source-egg/internal/fsx"
	"golang.org/x/sys/unix"
)

const maxInstallFileBytes = 1 << 30
const maxInstallJSONBytes = 16 << 20

type fileMutation struct {
	BeforeSHA string // Empty means the target was absent when planning.
	Path      string
	Source    string
	SHA256    string
	Remove    bool
}
type rollbackFile struct {
	Path    string `json:"path"`
	Existed bool   `json:"existed"`
	Backup  string `json:"backup,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	Mode    uint32 `json:"mode,omitempty"`
}
type installJournal struct {
	Version   int            `json:"version"`
	GameDir   string         `json:"game_dir"`
	Directory string         `json:"directory"`
	Undo      []rollbackFile `json:"undo"`
}

// installFS pins every directory with O_NOFOLLOW. Unlike a path-only symlink
// check, another process renaming a directory cannot redirect a later write.
// os.Root initially confines the configured volume; all descendants use openat.
type installFS struct{ base *os.File }

func openInstallFS(volume string) (*installFS, error) {
	root, err := os.OpenRoot(volume)
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
func (f *installFS) Close() error { return f.base.Close() }
func fsPathError(op, name string, err error) error {
	return &os.PathError{Op: op, Path: name, Err: err}
}
func (f *installFS) parent(name string, create bool) (*os.File, string, error) {
	if !cleanRelative(name, false) {
		return nil, "", errors.New("unsafe plugin filesystem path")
	}
	fd, err := unix.Dup(int(f.base.Fd()))
	if err != nil {
		return nil, "", err
	}
	unix.CloseOnExec(fd)
	current := os.NewFile(uintptr(fd), "plugin volume")
	parts := strings.Split(name, "/")
	for _, part := range parts[:len(parts)-1] {
		next, err := unix.Openat(int(current.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.ENOENT) && create {
			err = unix.Mkdirat(int(current.Fd()), part, 0755)
			if err == nil {
				err = current.Sync()
			} else if errors.Is(err, unix.EEXIST) {
				err = nil
			}
			if err == nil {
				next, err = unix.Openat(int(current.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			}
		}
		current.Close()
		if err != nil {
			return nil, "", fsPathError("open parent", name, err)
		}
		current = os.NewFile(uintptr(next), part)
	}
	return current, parts[len(parts)-1], nil
}
func regularAt(parent *os.File, leaf, name string, private bool) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), leaf, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fsPathError("open", name, err)
	}
	result := os.NewFile(uintptr(fd), name)
	st, err := result.Stat()
	if err != nil {
		result.Close()
		return nil, err
	}
	if !st.Mode().IsRegular() {
		result.Close()
		return nil, fmt.Errorf("%s must be a regular file", name)
	}
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil {
		result.Close()
		return nil, err
	}
	if private && stat.Nlink != 1 {
		result.Close()
		return nil, fmt.Errorf("%s must not be hardlinked", name)
	}
	return result, nil
}
func (f *installFS) openRegular(name string, private bool) (*os.File, error) {
	parent, leaf, err := f.parent(name, false)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	return regularAt(parent, leaf, name, private)
}
func (f *installFS) digest(name string, private bool) (string, os.FileMode, error) {
	in, err := f.openRegular(name, private)
	if err != nil {
		return "", 0, err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(in, maxInstallFileBytes+1))
	if err != nil {
		return "", 0, err
	}
	if n > maxInstallFileBytes {
		return "", 0, fmt.Errorf("%s exceeds the backup size limit", name)
	}
	return hex.EncodeToString(h.Sum(nil)), st.Mode().Perm(), nil
}
func (f *installFS) readJSON(name string, result any) error {
	in, err := f.openRegular(name, true)
	if err != nil {
		return err
	}
	defer in.Close()
	data, err := io.ReadAll(io.LimitReader(in, maxInstallJSONBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxInstallJSONBytes {
		return errors.New("plugin metadata exceeds 16 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(result); err != nil {
		return err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return errors.New("plugin metadata contains trailing JSON")
	}
	return nil
}
func (f *installFS) noJournal() error {
	in, err := f.openRegular(journalPath, true)
	if os.IsNotExist(err) {
		return nil
	}
	if err == nil {
		in.Close()
		err = errors.New("interrupted transaction requires recovery")
	}
	return fmt.Errorf("%w: %v", ErrRecovery, err)
}

// atomic never writes through an existing inode, including shared node hardlinks.
// File data and the containing directory are synced before success is reported.
func (f *installFS) atomic(name string, in io.Reader, want string, mode os.FileMode) error {
	parent, leaf, err := f.parent(name, true)
	if err != nil {
		return err
	}
	defer parent.Close()
	check := func() error {
		existing, err := regularAt(parent, leaf, name, false)
		if err == nil {
			existing.Close()
		}
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err = check(); err != nil {
		return err
	}
	temp := ".plugin-tmp-" + fsx.Token()
	fd, err := unix.Openat(int(parent.Fd()), temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return fsPathError("create", name, err)
	}
	out := os.NewFile(uintptr(fd), temp)
	defer func() { out.Close(); unix.Unlinkat(int(parent.Fd()), temp, 0) }()
	h := sha256.New()
	n, err := io.Copy(out, io.TeeReader(io.LimitReader(in, maxInstallFileBytes+1), h))
	if err != nil {
		return err
	}
	if n > maxInstallFileBytes {
		return errors.New("plugin file exceeds 1 GiB")
	}
	if want != "" && hex.EncodeToString(h.Sum(nil)) != want {
		return fmt.Errorf("%s changed after verification", name)
	}
	if err = out.Chmod(mode.Perm()); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	if err = check(); err != nil {
		return err
	}
	if err = unix.Renameat(int(parent.Fd()), temp, int(parent.Fd()), leaf); err != nil {
		return fsPathError("rename", name, err)
	}
	return parent.Sync()
}
func (f *installFS) remove(name string) error {
	parent, leaf, err := f.parent(name, false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.Close()
	current, err := regularAt(parent, leaf, name, false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	current.Close()
	if err = unix.Unlinkat(int(parent.Fd()), leaf, 0); err != nil {
		return fsPathError("remove", name, err)
	}
	return parent.Sync()
}
func (f *installFS) removeDir(name string) error {
	parent, leaf, err := f.parent(name, false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer parent.Close()
	if err = unix.Unlinkat(int(parent.Fd()), leaf, unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) {
		return fsPathError("remove directory", name, err)
	}
	return parent.Sync()
}
func jsonBytes(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(data) > maxInstallJSONBytes {
		return nil, errors.New("plugin metadata exceeds 16 MiB")
	}
	return append(data, '\n'), nil
}

func (i Installer) Recover() error {
	f, err := i.open()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRecovery, err)
	}
	defer f.Close()
	var journal installJournal
	if err = f.readJSON(journalPath, &journal); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("%w: read journal: %v", ErrRecovery, err)
	}
	if err = i.restore(f, journal); err != nil {
		return fmt.Errorf("%w: %v", ErrRecovery, err)
	}
	return nil
}
func (i Installer) validateJournal(j installJournal) error {
	token, ok := strings.CutPrefix(j.Directory, ".source/plugins/transactions/")
	decoded, err := hex.DecodeString(token)
	if !ok || err != nil || len(decoded) != 16 || strings.ToLower(token) != token || j.Version != 1 || j.GameDir != i.GameDir || len(j.Undo) == 0 || len(j.Undo) > 100001 {
		return errors.New("invalid plugin transaction journal")
	}
	seen := map[string]bool{}
	for index, entry := range j.Undo {
		rel, gameFile := strings.CutPrefix(entry.Path, i.GameDir+"/")
		if !(gameFile && ValidTarget(rel) && strings.Contains(rel, "/")) && entry.Path != installedPath {
			return fmt.Errorf("unsafe recovery target %q", entry.Path)
		}
		if seen[entry.Path] {
			return errors.New("duplicate recovery target")
		}
		seen[entry.Path] = true
		if entry.Existed {
			if entry.Backup != j.Directory+"/"+strconv.Itoa(index) || !validDigest(entry.SHA256) || entry.Mode > 0777 {
				return errors.New("invalid recovery backup")
			}
		} else if entry.Backup != "" || entry.SHA256 != "" || entry.Mode != 0 {
			return errors.New("invalid absent recovery target")
		}
	}
	if j.Undo[len(j.Undo)-1].Path != installedPath {
		return errors.New("journal lacks final receipt recovery")
	}
	return nil
}
func (i Installer) restore(f *installFS, j installJournal) error {
	if err := i.validateJournal(j); err != nil {
		return err
	}
	// Check every backup and destination before making recovery itself partial.
	for _, entry := range j.Undo {
		if entry.Existed {
			digest, _, err := f.digest(entry.Backup, true)
			if err != nil {
				return err
			}
			if digest != entry.SHA256 {
				return fmt.Errorf("backup for %s has changed", entry.Path)
			}
		}
		current, err := f.openRegular(entry.Path, entry.Path == installedPath)
		if err == nil {
			current.Close()
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	// Restore the receipt last: it describes the game files restored before it.
	for _, entry := range j.Undo {
		if !entry.Existed {
			if err := f.remove(entry.Path); err != nil {
				return err
			}
			continue
		}
		backup, err := f.openRegular(entry.Backup, true)
		if err != nil {
			return err
		}
		err = f.atomic(entry.Path, backup, entry.SHA256, os.FileMode(entry.Mode))
		backup.Close()
		if err != nil {
			return err
		}
	}
	if err := f.remove(journalPath); err != nil {
		return err
	}
	f.cleanup(j)
	return nil
}
func (f *installFS) cleanup(j installJournal) {
	for _, entry := range j.Undo {
		if entry.Existed {
			_ = f.remove(entry.Backup)
		}
	}
	_ = f.removeDir(j.Directory)
}
func (i Installer) prepareJournal(f *installFS, mutations []fileMutation) (installJournal, error) {
	j := installJournal{Version: 1, GameDir: i.GameDir, Directory: ".source/plugins/transactions/" + fsx.Token()}
	targets := make([]string, 0, len(mutations)+1)
	for _, mutation := range mutations {
		targets = append(targets, mutation.Path)
	}
	targets = append(targets, installedPath)
	for index, name := range targets {
		entry := rollbackFile{Path: name}
		in, err := f.openRegular(name, name == installedPath)
		if os.IsNotExist(err) {
			if index < len(mutations) && mutations[index].BeforeSHA != "" {
				f.cleanup(j)
				return j, fmt.Errorf("%s changed after planning", name)
			}
			j.Undo = append(j.Undo, entry)
			continue
		}
		if err != nil {
			f.cleanup(j)
			return j, err
		}
		st, err := in.Stat()
		if err != nil {
			in.Close()
			f.cleanup(j)
			return j, err
		}
		entry.Existed = true
		entry.Backup = j.Directory + "/" + strconv.Itoa(index)
		entry.Mode = uint32(st.Mode().Perm())
		h := sha256.New()
		err = f.atomic(entry.Backup, io.TeeReader(in, h), "", 0600)
		in.Close()
		if err != nil {
			f.cleanup(j)
			return j, err
		}
		entry.SHA256 = hex.EncodeToString(h.Sum(nil))
		j.Undo = append(j.Undo, entry)
		if index < len(mutations) && mutations[index].BeforeSHA != entry.SHA256 {
			f.cleanup(j)
			return j, fmt.Errorf("%s changed after planning", name)
		}
	}
	if err := i.validateJournal(j); err != nil {
		f.cleanup(j)
		return j, err
	}
	data, err := jsonBytes(j)
	if err != nil {
		f.cleanup(j)
		return j, err
	}
	if err = f.atomic(journalPath, bytes.NewReader(data), "", 0600); err != nil {
		// Rename can succeed before a directory-sync error. If a journal exists,
		// keep its backups so the next startup can recover it.
		in, readErr := f.openRegular(journalPath, true)
		if readErr == nil {
			in.Close()
			return j, fmt.Errorf("%w: persist journal: %v", ErrRecovery, err)
		}
		if os.IsNotExist(readErr) {
			f.cleanup(j)
			return j, err
		}
		return j, fmt.Errorf("%w: persist journal: %v", ErrRecovery, err)
	}
	return j, nil
}
func (i Installer) transact(f *installFS, mutations []fileMutation, receipt installationReceipt) error {
	if err := f.noJournal(); err != nil {
		return err
	}
	data, err := jsonBytes(receipt)
	if err != nil {
		return err
	}
	journal, err := i.prepareJournal(f, mutations)
	if err != nil {
		return err
	}
	fail := func(cause error) error {
		if rollbackErr := i.restore(f, journal); rollbackErr != nil {
			return fmt.Errorf("%w: deployment failed: %v; rollback failed: %v", ErrRecovery, cause, rollbackErr)
		}
		return fmt.Errorf("plugin deployment rolled back: %w", cause)
	}
	for _, mutation := range mutations {
		if err = copyMutation(f, mutation); err != nil {
			return fail(err)
		}
	}
	if err = f.atomic(installedPath, bytes.NewReader(data), "", 0600); err != nil {
		return fail(err)
	}
	// Removing and syncing the journal is the commit point. Backups are deleted
	// only after that point, so a resurrected pre-commit journal is recoverable.
	if err = f.remove(journalPath); err != nil {
		// Re-publish undo state before rollback if unlink completed but fsync failed.
		encoded, encodeErr := jsonBytes(journal)
		if encodeErr == nil {
			encodeErr = f.atomic(journalPath, bytes.NewReader(encoded), "", 0600)
		}
		if encodeErr != nil {
			return fmt.Errorf("%w: commit journal: %v; restore journal: %v", ErrRecovery, err, encodeErr)
		}
		return fail(err)
	}
	f.cleanup(journal)
	return nil
}
