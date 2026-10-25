package metrics

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// Top uses Docker samples already collected by the node, without an extra stream.
func Top(ctx context.Context, input *os.File, output io.Writer, get func(context.Context) (Snapshot, error)) error {
	fd := int(input.Fd())
	term, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		s, e := get(ctx)
		if e != nil {
			return e
		}
		_, e = io.WriteString(output, Render(s, "cpu", 0, 120, 50))
		return e
	}
	raw := *term
	raw.Lflag &^= unix.ICANON | unix.ECHO
	raw.Cc[unix.VMIN] = 0
	raw.Cc[unix.VTIME] = 1
	if err = unix.IoctlSetTermios(fd, unix.TCSETS, &raw); err != nil {
		return err
	}
	defer unix.IoctlSetTermios(fd, unix.TCSETS, term)
	fmt.Fprint(output, "\x1b[?25l")
	defer fmt.Fprint(output, "\x1b[?25h\n")
	order := 0
	orders := []string{"cpu", "players", "memory", "name"}
	offset := 0
	last := time.Time{}
	redraw := true
	var sample Snapshot
	for {
		if ctx.Err() != nil {
			return nil
		}
		if redraw || time.Since(last) >= time.Second {
			s, e := get(ctx)
			if e != nil {
				return e
			}
			sample = s
			last = time.Now()
			width, height := 120, 24
			if size, e := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ); e == nil {
				width, height = int(size.Col), int(size.Row)
			}
			fmt.Fprint(output, "\x1b[H\x1b[2J", Render(sample, orders[order], offset, width, height))
			redraw = false
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		_, e := unix.Poll(fds, 100)
		if e == unix.EINTR {
			continue
		}
		if e != nil {
			return e
		}
		if fds[0].Revents&(unix.POLLHUP|unix.POLLERR) != 0 {
			return nil
		}
		if fds[0].Revents&unix.POLLIN == 0 {
			continue
		}
		var buf [32]byte
		n, e := unix.Read(fd, buf[:])
		if e != nil {
			return e
		}
		if n == 0 {
			continue
		}
		key := string(buf[:n])
		switch key {
		case "q", "Q", "\x03":
			return nil
		case "s", "S":
			order = (order + 1) % len(orders)
		case "\x1b[A":
			offset = max(0, offset-1)
		case "\x1b[B":
			offset = min(max(0, len(sample.Servers)-1), offset+1)
		case "\x1b[5~":
			offset = max(0, offset-10)
		case "\x1b[6~":
			offset = min(max(0, len(sample.Servers)-1), offset+10)
		}
		redraw = true
	}
}
