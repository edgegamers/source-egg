//go:build !linux

package fsx

import (
	"errors"
	"os"
)

func openReceipt(root *os.Root, name string) (*os.File, error) {
	st, e := root.Lstat(name)
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("JSON receipt is not regular")
	}
	f, e := root.Open(name)
	if e != nil {
		return nil, e
	}
	opened, e := f.Stat()
	if e != nil || !os.SameFile(st, opened) {
		f.Close()
		return nil, errors.New("JSON receipt changed")
	}
	return f, nil
}
