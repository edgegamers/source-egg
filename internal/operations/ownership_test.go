//go:build linux

package operations

import (
	"io/fs"
	"syscall"
)

func ownerIDs(info fs.FileInfo) (uint32, uint32) {
	st := info.Sys().(*syscall.Stat_t)
	return st.Uid, st.Gid
}
