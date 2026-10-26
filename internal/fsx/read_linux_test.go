package fsx

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestReadJSONRejectsUnsafeReceipts(t *testing.T) {
	for _, kind := range []string{"fifo", "symlink", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			name := filepath.Join(dir, "receipt")
			switch kind {
			case "fifo":
				if e := unix.Mkfifo(name, 0600); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				if e := os.WriteFile(filepath.Join(dir, "target"), []byte(`{}`), 0600); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink("target", name); e != nil {
					t.Fatal(e)
				}
			case "oversized":
				f, e := os.Create(name)
				if e != nil {
					t.Fatal(e)
				}
				f.WriteString(`{}`)
				if _, e = f.Write(bytes.Repeat([]byte(" "), 16<<20)); e != nil {
					t.Fatal(e)
				}
				f.Close()
			}
			root, e := os.OpenRoot(dir)
			if e != nil {
				t.Fatal(e)
			}
			defer root.Close()
			done := make(chan error, 1)
			go func() { var v any; done <- ReadJSON(root, "receipt", &v) }()
			select {
			case e := <-done:
				if e == nil {
					t.Fatal("unsafe receipt accepted")
				}
			case <-time.After(time.Second):
				// Release a legacy blocking FIFO reader so the regression fails cleanly.
				if kind == "fifo" {
					f, e := os.OpenFile(name, os.O_RDWR|unix.O_NONBLOCK, 0)
					if e == nil {
						f.WriteString(`{}`)
						f.Close()
					}
					<-done
				}
				t.Fatal("receipt read blocked")
			}
		})
	}
}
