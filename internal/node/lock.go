package node

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

func (d *Daemon) lock() (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(d.Config.StateDir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("another sourcenode owns this state directory: %w", err)
	}
	return file, nil
}
