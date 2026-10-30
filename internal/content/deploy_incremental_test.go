package content

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/edgegamers/source-egg/internal/fsx"
	"github.com/edgegamers/source-egg/internal/game"
)

func tf2DeliveryFixture(t testing.TB) (Generation, string, string) {
	t.Helper()
	profile, _ := game.Lookup("tf2")
	id, _ := profile.Identity("public")
	store := &Store{Dir: t.TempDir(), Installer: &fixtureInstaller{build: "100"}}
	g, err := store.Ensure(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(g.Path, "tf/maps"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(g.Path, "tf/maps/stock.bsp"), []byte("stock map"), 0644); err != nil {
		t.Fatal(err)
	}
	return g, t.TempDir(), store.Dir
}

func TestRepeatedDeploymentReusesUnchangedStockFiles(t *testing.T) {
	for _, method := range []string{"symlink", "hardlink", "copy"} {
		t.Run(method, func(t *testing.T) {
			g, volume, shared := tf2DeliveryFixture(t)
			opts := DeployOptions{Method: method}
			if err := DeployWithOptions(g, volume, shared, opts); err != nil {
				t.Fatal(err)
			}
			// Keep the original inode alive so replacement cannot reuse it.
			original, err := os.Open(filepath.Join(volume, "tf/maps/stock.bsp"))
			if err != nil {
				t.Fatal(err)
			}
			defer original.Close()
			before, err := original.Stat()
			if err != nil {
				t.Fatal(err)
			}
			if err := DeployWithOptions(g, volume, shared, opts); err != nil {
				t.Fatal(err)
			}
			after, err := os.Stat(filepath.Join(volume, "tf/maps/stock.bsp"))
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
				t.Fatal("unchanged stock map was copied again on restart")
			}
		})
	}
}

func TestRepeatedDeploymentRepairsChangedStockFiles(t *testing.T) {
	for _, change := range []string{"missing", "edited", "preserved-mtime", "permissions", "symlink"} {
		t.Run(change, func(t *testing.T) {
			g, volume, shared := tf2DeliveryFixture(t)
			if err := Deploy(g, volume, shared); err != nil {
				t.Fatal(err)
			}
			stock := filepath.Join(volume, "tf/maps/stock.bsp")
			outside := filepath.Join(t.TempDir(), "outside")
			if err := os.WriteFile(outside, []byte("untouched"), 0644); err != nil {
				t.Fatal(err)
			}
			var err error
			switch change {
			case "missing":
				err = os.Remove(stock)
			case "edited":
				err = os.WriteFile(stock, []byte("local map"), 0644)
				if err == nil {
					err = os.Chtimes(stock, time.Unix(100, 0), time.Unix(100, 0))
				}
			case "preserved-mtime":
				before, statErr := os.Stat(stock)
				if statErr != nil {
					t.Fatal(statErr)
				}
				err = os.WriteFile(stock, []byte("local map"), 0644)
				if err == nil {
					err = os.Chtimes(stock, before.ModTime(), before.ModTime())
				}
			case "permissions":
				err = os.Chmod(stock, 0600)
			case "symlink":
				err = os.Remove(stock)
				if err == nil {
					err = os.Symlink(outside, stock)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := Deploy(g, volume, shared); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(stock)
			if err != nil || string(data) != "stock map" {
				t.Fatalf("stock map not repaired: %q, %v", data, err)
			}
			info, err := os.Lstat(stock)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0644 {
				t.Fatalf("stock map mode not repaired: %v, %v", info, err)
			}
			data, err = os.ReadFile(outside)
			if err != nil || string(data) != "untouched" {
				t.Fatalf("followed destination symlink: %q, %v", data, err)
			}
		})
	}
}

func TestDeploymentUpdatesStockFilesWhenGenerationChanges(t *testing.T) {
	g, volume, shared := tf2DeliveryFixture(t)
	if err := Deploy(g, volume, shared); err != nil {
		t.Fatal(err)
	}
	profile, _ := game.Lookup("tf2")
	id, _ := profile.Identity("public")
	store := &Store{Dir: shared, Installer: &fixtureInstaller{build: "200"}}
	next, err := store.Ensure(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	// Even identical size and timestamps must not hide a generation update.
	stock := filepath.Join(next.Path, "tf/maps/stock.bsp")
	if err := os.WriteFile(stock, []byte("newer map"), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(filepath.Join(g.Path, "tf/maps/stock.bsp"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(stock, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := Deploy(next, volume, shared); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(volume, "tf/maps/stock.bsp"))
	if err != nil || string(data) != "newer map" {
		t.Fatalf("stock map did not update: %q, %v", data, err)
	}
}

func TestLegacyDeploymentReceiptRemainsSupported(t *testing.T) {
	g, volume, shared := tf2DeliveryFixture(t)
	if err := Deploy(g, volume, shared); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(volume, ".source/managed.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Retain only the fields written by older nodes.
	var old struct {
		Generation Generation `json:"generation"`
		Files      []string   `json:"files"`
	}
	if err := json.Unmarshal(data, &old); err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Deploy(g, volume, shared); err != nil {
		t.Fatal(err)
	}
	original, err := os.Open(filepath.Join(volume, "tf/maps/stock.bsp"))
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	before, err := original.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := Deploy(g, volume, shared); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(filepath.Join(volume, "tf/maps/stock.bsp"))
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("legacy receipt did not upgrade to reusable copies: %v", err)
	}
}

func BenchmarkRepeatedTF2Deployment(b *testing.B) {
	g, volume, shared := tf2DeliveryFixture(b)
	if err := os.WriteFile(filepath.Join(g.Path, "tf/maps/stock.bsp"), []byte(strings.Repeat("stock map data!\n", 1<<20)), 0644); err != nil {
		b.Fatal(err)
	}
	if err := Deploy(g, volume, shared); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		if err := Deploy(g, volume, shared); err != nil {
			b.Fatal(err)
		}
	}
}

func TestCopyMetadataCannotMakeDeploymentReceiptUnreadable(t *testing.T) {
	g, volume, _ := tf2DeliveryFixture(t)
	info, err := os.Stat(filepath.Join(g.Path, "tf/maps/stock.bsp"))
	if err != nil {
		t.Fatal(err)
	}
	next := Deployment{Generation: g, Copies: map[string]copiedFile{}}
	for i := range 40000 {
		name := fmt.Sprintf("tf/materials/stock/asset_%05d.vmt", i)
		next.Files = append(next.Files, name)
		next.Copies[name] = copiedFile{Source: copyState(info), Destination: copyState(info)}
	}
	root, err := os.OpenRoot(volume)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.Mkdir(".source", 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeDeploymentReceipt(root, next, nil); err != nil {
		t.Fatal(err)
	}
	var restored Deployment
	if err := fsx.ReadJSON(root, ".source/managed.json", &restored); err != nil {
		t.Fatalf("successful deployment wrote a receipt that prevents the next start: %v", err)
	}
	if len(restored.Files) != 40000 || restored.Generation.Key != g.Key {
		t.Fatal("receipt lost authoritative inventory while limiting optional metadata")
	}
}
