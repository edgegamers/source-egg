package node

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMountChild(t *testing.T) {
	if root := os.Getenv("SOURCE_EGG_MOUNT_CHILD"); root != "" {
		if err := syscall.Chroot(root); err != nil {
			os.Exit(3)
		}
		os.Chdir("/")
		fmt.Println("READY")
		bufio.NewReader(os.Stdin).ReadString('\n')
		os.Exit(0)
	}
}
func TestReadOnlyMountTargetsContainerRoot(t *testing.T) {
	if os.Getenv("SOURCE_EGG_MOUNT_TEST") != "1" {
		t.Skip("requires isolated Linux container with SYS_ADMIN")
	}
	root := t.TempDir()
	source := t.TempDir()
	os.MkdirAll(filepath.Join(root, "shared"), 0755)
	os.WriteFile(filepath.Join(source, "probe"), []byte("immutable"), 0644)
	child := exec.Command(os.Args[0], "-test.run=^TestMountChild$")
	child.Env = append(os.Environ(), "SOURCE_EGG_MOUNT_CHILD="+root)
	child.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNS}
	in, _ := child.StdinPipe()
	out, _ := child.StdoutPipe()
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { in.Write([]byte("quit\n")); child.Wait() }()
	if line, _ := bufio.NewReader(out).ReadString('\n'); line != "READY\n" {
		t.Fatal(line)
	}
	if err := BindReadOnly(child.Process.Pid, source, "/shared"); err != nil {
		t.Fatal(err)
	}
	target := fmt.Sprintf("/proc/%d/root/shared/probe", child.Process.Pid)
	b, err := os.ReadFile(target)
	if err != nil || string(b) != "immutable" {
		t.Fatal(string(b), err)
	}
	if err = os.WriteFile(target, []byte("corrupt"), 0644); err == nil {
		t.Fatal("shared tree was writable")
	}
}
