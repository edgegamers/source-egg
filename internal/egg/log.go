package egg

import (
	"fmt"
	"os"
)

type RotatingLog struct {
	root      *os.Root
	file      *os.File
	size, max int64
	keep      int
}

func OpenLog(dir string, max int64, keep int) (*RotatingLog, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	l := &RotatingLog{root: root, max: max, keep: keep}
	if err = l.open(); err != nil {
		root.Close()
		return nil, err
	}
	return l, nil
}
func (l *RotatingLog) open() error {
	f, err := l.root.OpenFile("console.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.file = f
	l.size = info.Size()
	return nil
}
func (l *RotatingLog) Write(data []byte) (int, error) {
	if l.size > 0 && l.size+int64(len(data)) > l.max {
		if err := l.file.Close(); err != nil {
			return 0, err
		}
		l.root.Remove(fmt.Sprintf("console.%d.log", l.keep))
		for i := l.keep - 1; i >= 1; i-- {
			if err := l.root.Rename(fmt.Sprintf("console.%d.log", i), fmt.Sprintf("console.%d.log", i+1)); err != nil && !os.IsNotExist(err) {
				return 0, err
			}
		}
		if err := l.root.Rename("console.log", "console.1.log"); err != nil {
			return 0, err
		}
		if err := l.open(); err != nil {
			return 0, err
		}
	}
	n, err := l.file.Write(data)
	l.size += int64(n)
	return n, err
}
func (l *RotatingLog) Close() error { err := l.file.Close(); l.root.Close(); return err }
