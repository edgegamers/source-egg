package egg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConsoleRotationBoundsDiskAndPreservesNewest(t *testing.T) {
	dir := t.TempDir()
	log, err := OpenLog(dir, 8, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"first---", "second--", "third---", "fourth--"} {
		if _, err = log.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	log.Close()
	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Fatal("unbounded logs", len(entries))
	}
	for name, want := range map[string]string{"console.log": "fourth--", "console.1.log": "third---", "console.2.log": "second--"} {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		if string(b) != want {
			t.Fatal(name, string(b))
		}
	}
}
