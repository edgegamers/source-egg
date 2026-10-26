// Package fsx provides atomic, root-confined writes for untrusted server volumes.
package fsx

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func Token() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// Atomic replaces the directory entry, never follows the destination symlink.
func Atomic(root *os.Root, name string, data []byte, mode fs.FileMode) error {
	if !filepath.IsLocal(name) {
		return errors.New("non-local destination")
	}
	if err := root.MkdirAll(filepath.Dir(name), 0755); err != nil {
		return err
	}
	tmp := name + ".tmp-" + Token()
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return root.Rename(tmp, name)
}
func JSON(root *os.Root, name string, v any, mode fs.FileMode) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return Atomic(root, name, append(b, '\n'), mode)
}
func Copy(root *os.Root, name, source string, mode fs.FileMode) error {
	in, e := os.Open(source)
	if e != nil {
		return e
	}
	defer in.Close()
	if !filepath.IsLocal(name) {
		return errors.New("non-local destination")
	}
	if e = root.MkdirAll(filepath.Dir(name), 0755); e != nil {
		return e
	}
	tmp := name + ".tmp-" + Token()
	out, e := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if e != nil {
		return e
	}
	defer root.Remove(tmp)
	_, e = io.Copy(out, in)
	if e == nil {
		e = out.Sync()
	}
	c := out.Close()
	if e != nil {
		return e
	}
	if c != nil {
		return c
	}
	return root.Rename(tmp, name)
}
func ReadJSON(root *os.Root, name string, v any) error {
	f, e := openReceipt(root, name)
	if e != nil {
		return e
	}
	defer f.Close()
	const maxJSONBytes = 16 << 20
	st, e := f.Stat()
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() || st.Size() > maxJSONBytes {
		return errors.New("JSON receipt must be a regular file of at most 16 MiB")
	}
	b, e := io.ReadAll(io.LimitReader(f, maxJSONBytes+1))
	if e != nil {
		return e
	}
	if len(b) > maxJSONBytes {
		return errors.New("JSON receipt exceeds 16 MiB")
	}
	return json.Unmarshal(b, v)
}
