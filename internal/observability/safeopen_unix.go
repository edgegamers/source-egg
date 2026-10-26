//go:build unix

package observability

import "golang.org/x/sys/unix"

const noFollowFlags = unix.O_NOFOLLOW | unix.O_NONBLOCK
