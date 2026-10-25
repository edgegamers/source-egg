package observability

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// privateRoot opens each component without accepting directory symlinks. Roots
// are short lived, so replacement of the configured directory is rechecked on
// every operation. All descendants remain confined by os.Root.
func privateRoot(path string) (*os.Root, error) {
	return directoryRoot(path, true)
}

// directoryRoot pins every ancestor before advancing to its child. Read-only
// server volumes use create=false, so opening a missing volume cannot create it.
func directoryRoot(path string, create bool) (*os.Root, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("storage path must be absolute")
	}
	path = filepath.Clean(path)
	if protectedStorage(path) {
		return nil, errors.New("protected storage directory")
	}
	base := filepath.VolumeName(path) + string(filepath.Separator)
	r, e := os.OpenRoot(base)
	if e != nil {
		return nil, e
	}
	rel, e := filepath.Rel(base, path)
	if e != nil {
		r.Close()
		return nil, e
	}
	if rel == "." {
		r.Close()
		return nil, errors.New("storage cannot be filesystem root")
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		next, e := subRoot(r, part, create)
		r.Close()
		if e != nil {
			return nil, e
		}
		r = next
	}
	return r, nil
}

// boundedWalk enumerates small chunks instead of letting fs.WalkDir load and
// sort an attacker-controlled directory. The global budget includes old files
// and ignored entries; DFS retains at most 32 rooted directory descriptors.
func boundedWalk(ctx context.Context, root *os.Root, budget int, visit func(string, fs.DirEntry) error) error {
	type frame struct {
		root    *os.Root
		file    *os.File
		prefix  string
		entries []fs.DirEntry
		index   int
		owned   bool
		eof     bool
	}
	f, e := root.Open(".")
	if e != nil {
		return e
	}
	stack := []frame{{root: root, file: f}}
	defer func() {
		for _, s := range stack {
			s.file.Close()
			if s.owned {
				s.root.Close()
			}
		}
	}()
	visited := 0
	for len(stack) > 0 {
		if e := ctx.Err(); e != nil {
			return e
		}
		if visited >= budget {
			return nil
		}
		top := &stack[len(stack)-1]
		if top.index >= len(top.entries) {
			if top.eof {
				top.file.Close()
				if top.owned {
					top.root.Close()
				}
				stack = stack[:len(stack)-1]
				continue
			}
			entries, e := top.file.ReadDir(min(128, budget-visited))
			if e != nil && e != io.EOF {
				return e
			}
			top.entries = entries
			top.index = 0
			top.eof = e == io.EOF
			if len(entries) == 0 {
				continue
			}
		}
		d := top.entries[top.index]
		top.index++
		visited++
		path := d.Name()
		if top.prefix != "" {
			path = top.prefix + "/" + path
		}
		e := visit(path, d)
		if e == fs.SkipAll {
			return nil
		}
		if e == fs.SkipDir {
			continue
		}
		if e != nil {
			return e
		}
		if !d.IsDir() || d.Type()&os.ModeSymlink != 0 || len(stack) >= 32 {
			continue
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		child, e := subRoot(top.root, d.Name(), false)
		if e != nil {
			continue
		}
		file, e := child.Open(".")
		if e != nil {
			child.Close()
			continue
		}
		stack = append(stack, frame{root: child, file: file, prefix: path, owned: true})
	}
	return nil
}
func subRoot(parent *os.Root, name string, create bool) (*os.Root, error) {
	if !idPattern.MatchString(name) && !safeComponent(name) {
		return nil, errors.New("unsafe directory component")
	}
	st, e := parent.Lstat(name)
	if os.IsNotExist(e) && create {
		if e = parent.Mkdir(name, 0700); e != nil && !os.IsExist(e) {
			return nil, e
		}
		st, e = parent.Lstat(name)
	}
	if e != nil {
		return nil, e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("storage directory is not a real directory")
	}
	r, e := parent.OpenRoot(name)
	if e != nil {
		return nil, e
	}
	opened, e := r.Stat(".")
	if e != nil || !os.SameFile(st, opened) {
		r.Close()
		return nil, errors.New("storage directory changed")
	}
	return r, nil
}
func safeComponent(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, "/\\\x00")
}
func regularOpen(r *os.Root, p string) (*os.File, error) {
	st, e := r.Lstat(p)
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	f, e := r.OpenFile(p, os.O_RDONLY|noFollowFlags, 0)
	if e != nil {
		return nil, e
	}
	opened, e := f.Stat()
	if e != nil || !opened.Mode().IsRegular() || !os.SameFile(st, opened) {
		f.Close()
		return nil, errors.New("file changed")
	}
	return f, nil
}
func readDir(r *os.Root) ([]fs.DirEntry, error) {
	f, e := r.Open(".")
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return f.ReadDir(-1)
}
func serverRoots(root *os.Root, id string, fn func(string, *os.Root) error) error {
	if id != "" {
		if !idPattern.MatchString(id) {
			return errors.New("invalid server identity")
		}
		r, e := subRoot(root, id, false)
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		defer r.Close()
		return fn(id, r)
	}
	entries, e := readDir(root)
	if e != nil {
		return e
	}
	for _, d := range entries {
		if !idPattern.MatchString(d.Name()) || !d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			continue
		}
		r, e := subRoot(root, d.Name(), false)
		if e != nil {
			continue
		}
		e = fn(d.Name(), r)
		r.Close()
		if e != nil {
			return e
		}
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(b)
}
