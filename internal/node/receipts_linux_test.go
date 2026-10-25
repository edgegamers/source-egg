package node

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestObservationRejectsFIFOReceiptPromptly(t *testing.T) {
	dir := t.TempDir()
	if e := os.Mkdir(filepath.Join(dir, ".source"), 0700); e != nil {
		t.Fatal(e)
	}
	name := filepath.Join(dir, ".source/framework-metamod.json")
	if e := unix.Mkfifo(name, 0600); e != nil {
		t.Fatal(e)
	}
	d := &Daemon{states: map[string]State{}}
	done := make(chan struct{})
	go func() { d.observationServer(Server{UUID: "server", Volume: dir}); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		f, e := os.OpenFile(name, os.O_RDWR|unix.O_NONBLOCK, 0)
		if e == nil {
			f.WriteString(`{}`)
			f.Close()
		}
		<-done
		t.Fatal("observation blocked on container FIFO receipt")
	}
}
