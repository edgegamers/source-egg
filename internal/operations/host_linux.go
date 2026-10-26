//go:build linux

package operations

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func diskInfo(p string) (DiskInfo, error) {
	var s unix.Statfs_t
	if e := unix.Statfs(p, &s); e != nil {
		return DiskInfo{}, e
	}
	if s.Bsize <= 0 || s.Blocks == 0 {
		return DiskInfo{}, errors.New("invalid filesystem capacity")
	}
	return DiskInfo{Free: int64(s.Bavail) * s.Bsize, Total: int64(s.Blocks) * s.Bsize}, nil
}

// Inspect the container init and descendants; the game normally runs below the
// init process. Any unreadable descriptor inventory fails closed.
func processOpenFiles(ctx context.Context, s Server) (map[string]bool, error) {
	return processOpenFilesAt(ctx, s, "/proc")
}

func processOpenFilesAt(ctx context.Context, s Server, procRoot string) (map[string]bool, error) {
	held := map[string]bool{}
	if s.PID <= 0 {
		if s.Running {
			return nil, errors.New("running server PID unavailable")
		}
		return held, nil
	}
	root, e := os.OpenRoot(s.Volume)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	queue := []int{s.PID}
	seen := map[int]bool{}
	for len(queue) > 0 {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		if len(seen) > 4096 {
			return nil, errors.New("container process inventory exceeds bound")
		}
		proc := filepath.Join(procRoot, strconv.Itoa(pid))
		entries, e := os.ReadDir(proc + "/fd")
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return nil, e
		}
		for _, entry := range entries {
			target, e := os.Readlink(proc + "/fd/" + entry.Name())
			if errors.Is(e, os.ErrNotExist) {
				continue
			}
			if e != nil {
				return nil, e
			}
			target = strings.TrimSuffix(target, " (deleted)")
			// proc descriptor links describe the process's mount namespace. The
			// standard container mount and host volume identify the same files.
			// Read only the link text: never follow a descriptor into its target.
			if !filepath.IsAbs(target) || filepath.Clean(target) != target {
				continue
			}
			for _, base := range []string{s.Volume, "/home/container"} {
				rel, e := filepath.Rel(base, target)
				if e != nil || rel == "." || !filepath.IsLocal(rel) {
					continue
				}
				rel = filepath.ToSlash(rel)
				if !safeRel(rel) || noLinks(root, rel) != nil {
					continue
				}
				held[rel] = true
				break
			}
		}
		children, e := os.ReadFile(proc + "/task/" + strconv.Itoa(pid) + "/children")
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
		for _, raw := range strings.Fields(string(children)) {
			child, e := strconv.Atoi(raw)
			if e != nil {
				return nil, fmt.Errorf("invalid child PID: %w", e)
			}
			queue = append(queue, child)
		}
	}
	return held, nil
}

func inheritOwner(root *os.Root, out *os.File, name, volume string) error {
	st, e := root.Stat(name)
	if errors.Is(e, os.ErrNotExist) {
		st, e = root.Stat(path.Dir(name))
	}
	if e != nil {
		return e
	}
	v, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("ownership unavailable")
	}
	return out.Chown(int(v.Uid), int(v.Gid))
}

func inheritDirectoryOwner(root *os.Root, name string) error {
	st, e := root.Stat(path.Dir(name))
	if e != nil {
		return e
	}
	v, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("directory ownership unavailable")
	}
	return root.Chown(name, int(v.Uid), int(v.Gid))
}
