package fsx

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

var ErrWalkLimit = errors.New("directory traversal exceeds entry or depth bound")

// BoundedWalk visits unsorted entries in chunks of at most 128. Every entry,
// including ignored entries and directories, consumes the shared remaining
// budget. The starting directory is visited but does not consume an entry.
// Directory handles are pinned, links are never traversed, and depth is at most
// 32. The caller may reuse the budget across several walks in one operation.
func BoundedWalk(ctx context.Context, root *os.Root, start string, remaining *int, visit fs.WalkDirFunc) error {
	if !fs.ValidPath(start) || strings.Contains(start, "\\") || remaining == nil || *remaining < 0 {
		return errors.New("invalid bounded walk")
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	current := root
	owned := false
	defer func() {
		if owned {
			current.Close()
		}
	}()
	if start != "." {
		for _, part := range strings.Split(start, "/") {
			next, e := walkChild(current, part)
			if e != nil {
				return visit(start, nil, e)
			}
			if owned {
				current.Close()
			}
			current = next
			owned = true
		}
	}
	st, e := current.Stat(".")
	if e != nil {
		return visit(start, nil, e)
	}
	if e = visit(start, fs.FileInfoToDirEntry(st), nil); e == fs.SkipDir || e == fs.SkipAll {
		return nil
	} else if e != nil {
		return e
	}
	var walk func(*os.Root, string, int) error
	walk = func(dir *os.Root, prefix string, depth int) error {
		if depth > 32 {
			return ErrWalkLimit
		}
		f, e := dir.Open(".")
		if e != nil {
			return visit(prefix, nil, e)
		}
		defer f.Close()
		for {
			if e := ctx.Err(); e != nil {
				return e
			}
			entries, e := f.ReadDir(min(128, *remaining+1))
			if e != nil && e != io.EOF {
				return visit(prefix, nil, e)
			}
			for _, entry := range entries {
				if e := ctx.Err(); e != nil {
					return e
				}
				if *remaining == 0 {
					return ErrWalkLimit
				}
				*remaining--
				name := path.Join(prefix, entry.Name())
				e := visit(name, entry, nil)
				if e == fs.SkipDir {
					continue
				}
				if e != nil {
					return e
				}
				if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				child, e := walkChild(dir, entry.Name())
				if e != nil {
					if e = visit(name, nil, e); e != nil {
						return e
					}
					continue
				}
				e = walk(child, name, depth+1)
				child.Close()
				if e != nil {
					return e
				}
			}
			if e == io.EOF {
				return nil
			}
		}
	}
	e = walk(current, start, 1)
	if e == fs.SkipAll {
		return nil
	}
	return e
}

func walkChild(parent *os.Root, name string) (*os.Root, error) {
	st, e := parent.Lstat(name)
	if e != nil {
		return nil, e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("walk directory is not a real directory")
	}
	child, e := parent.OpenRoot(name)
	if e != nil {
		return nil, e
	}
	opened, e := child.Stat(".")
	if e != nil || !os.SameFile(st, opened) {
		child.Close()
		return nil, errors.New("walk directory changed")
	}
	return child, nil
}
