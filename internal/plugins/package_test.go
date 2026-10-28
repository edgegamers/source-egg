package plugins

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type packageEntry struct {
	name, body string
	mode       os.FileMode
	kind       byte
	size       int64
}

func packageZIP(t *testing.T, entries []packageEntry) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "package.zip")
	f, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(f)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		mode := entry.mode
		if mode == 0 {
			mode = 0644
		}
		header.SetMode(mode)
		out, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(out, entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return filename
}

func packageTar(t *testing.T, entries []packageEntry) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "package.tar.gz")
	f, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	writer := tar.NewWriter(gz)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		size := int64(len(entry.body))
		if entry.size != 0 {
			size = entry.size
		}
		if err := writer.WriteHeader(&tar.Header{Name: entry.name, Mode: 0644, Size: size, Typeflag: kind, Linkname: "elsewhere"}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(writer, entry.body); err != nil {
			t.Fatal(err)
		}
		if entry.size != 0 && entry.size != int64(len(entry.body)) {
			break
		} // Deliberately truncated oversized member.
	}
	_ = writer.Close()
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return filename
}

func packageRecipe() Plugin {
	return Plugin{ID: "example", Source: Source{Provider: "github", Repository: "owner/repo"}, Asset: "package.zip", Format: "zip", Mappings: []Mapping{{From: "release", To: "."}}}
}

func TestPreparePackageMapsSubtreesAndMarksSeeds(t *testing.T) {
	archive := packageZIP(t, []packageEntry{{name: "release/addons/sourcemod/plugins/plugin.smx", body: "hello"}, {name: "release/addons/sourcemod/translations/plugin.txt", body: "words"}, {name: "release/cfg/plugin.cfg", body: "default"}, {name: "release/addons/sourcemod/data/example.txt", body: "data"}, {name: "README.md", body: "not mapped"}})
	p := packageRecipe()
	p.Preserve = []string{"addons/sourcemod/translations/plugin.txt"}
	dest := filepath.Join(t.TempDir(), "stage")
	files, err := PreparePackage(archive, dest, p, "package.zip")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("mapped %d files: %#v", len(files), files)
	}
	file := files["addons/sourcemod/plugins/plugin.smx"]
	if file.SHA256 != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" || file.Seed {
		t.Fatalf("plugin metadata = %#v", file)
	}
	for _, name := range []string{"cfg/plugin.cfg", "addons/sourcemod/data/example.txt", "addons/sourcemod/translations/plugin.txt"} {
		if !files[name].Seed {
			t.Errorf("%s is not seed-only", name)
		}
	}
	got, err := os.ReadFile(filepath.Join(dest, "addons/sourcemod/plugins/plugin.smx"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("payload = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "README.md")); !os.IsNotExist(err) {
		t.Fatal("unmapped file included")
	}
}

func TestPreparePackageSupportsTarAndRawRenaming(t *testing.T) {
	t.Run("tar", func(t *testing.T) {
		archive := packageTar(t, []packageEntry{{name: "./", kind: tar.TypeDir}, {name: "./plugins/one.smx", body: "hello"}, {name: "./gamedata/one.txt", body: "definitions"}})
		p := packageRecipe()
		p.Format = "auto"
		p.Mappings = []Mapping{{From: "plugins", To: "addons/sourcemod/plugins"}, {From: "gamedata", To: "addons/sourcemod/gamedata"}}
		dest := filepath.Join(t.TempDir(), "stage")
		files, err := PreparePackage(archive, dest, p, "build.tar.gz")
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 2 || files["addons/sourcemod/plugins/one.smx"].SHA256 != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
			t.Fatalf("mapped files=%#v", files)
		}
	})
	t.Run("raw", func(t *testing.T) {
		archive := filepath.Join(t.TempDir(), "download")
		if err := os.WriteFile(archive, []byte("hello"), 0600); err != nil {
			t.Fatal(err)
		}
		p := packageRecipe()
		p.Format = "raw"
		p.Mappings = []Mapping{{From: "published.smx", To: "addons/sourcemod/plugins/renamed.smx"}}
		dest := filepath.Join(t.TempDir(), "stage")
		files, err := PreparePackage(archive, dest, p, "published.smx")
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 1 || files["addons/sourcemod/plugins/renamed.smx"].SHA256 != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
			t.Fatalf("mapped files=%#v", files)
		}
	})
}

func TestPreparePackageRejectsUnsafeArchiveEntriesEvenWhenUnmapped(t *testing.T) {
	for _, entry := range []packageEntry{{name: "../escape", body: "bad"}, {name: "/absolute", body: "bad"}, {name: "/", mode: os.ModeDir | 0755}, {name: "release/../escape", body: "bad"}, {name: "release\\escape", body: "bad"}, {name: "unmapped-link", body: "../../outside", mode: os.ModeSymlink | 0777}, {name: "unmapped-link/", mode: os.ModeSymlink | 0777}, {name: "unmapped-pipe", mode: os.ModeNamedPipe | 0600}} {
		t.Run(entry.name, func(t *testing.T) {
			archive := packageZIP(t, []packageEntry{{name: "release/addons/example.smx", body: "valid"}, entry})
			dest := filepath.Join(t.TempDir(), "stage")
			if _, err := PreparePackage(archive, dest, packageRecipe(), "package.zip"); err == nil {
				t.Fatal("unsafe ZIP entry accepted")
			}
			if _, err := os.Stat(filepath.Join(dest, "addons/example.smx")); !os.IsNotExist(err) {
				t.Fatal("failed extraction left staged payload")
			}
		})
	}
	for _, kind := range []byte{tar.TypeLink, tar.TypeSymlink, tar.TypeFifo, tar.TypeChar} {
		archive := packageTar(t, []packageEntry{{name: "link", kind: kind}})
		p := packageRecipe()
		p.Format = "tar.gz"
		if _, err := PreparePackage(archive, filepath.Join(t.TempDir(), "stage"), p, "package.tar.gz"); err == nil {
			t.Errorf("tar entry type %d accepted", kind)
		}
	}
}

func TestPreparePackageRejectsDuplicateAndMissingMappings(t *testing.T) {
	archive := packageZIP(t, []packageEntry{{name: "plugins/a.smx", body: "one"}, {name: "other/b.smx", body: "two"}})
	for _, mappings := range [][]Mapping{
		{{From: "missing", To: "addons/missing"}},
		{{From: "plugins/a.smx", To: "addons/plugin.smx"}, {From: "other/b.smx", To: "addons/plugin.smx"}},
		{{From: "plugins/a.smx", To: "addons/plugin.smx"}, {From: "other/b.smx", To: "addons/plugin.smx/nested"}},
		{{From: "plugins/a.smx", To: "."}},
		{{From: ".", To: "."}}, // Root mapping would include files outside plugin payload areas.
	} {
		p := packageRecipe()
		p.Mappings = mappings
		dest := filepath.Join(t.TempDir(), "stage")
		if _, err := PreparePackage(archive, dest, p, "package.zip"); err == nil {
			t.Errorf("invalid mappings accepted: %#v", mappings)
		}
	}
	archive = packageZIP(t, []packageEntry{{name: "release/addons/a", body: "first"}, {name: "release/addons/a", body: "second"}})
	if _, err := PreparePackage(archive, filepath.Join(t.TempDir(), "stage"), packageRecipe(), "package.zip"); err == nil {
		t.Fatal("duplicate archive member accepted")
	}
}

func TestPreparePackageRejectsLinkedOrPopulatedDestination(t *testing.T) {
	archive := packageZIP(t, []packageEntry{{name: "release/addons/example.smx", body: "hello"}})
	outside := t.TempDir()
	parent := t.TempDir()
	linked := filepath.Join(parent, "linked")
	if err := os.Symlink(outside, linked); err != nil {
		t.Fatal(err)
	}
	for _, dest := range []string{linked, filepath.Join(linked, "child")} {
		if _, err := PreparePackage(archive, dest, packageRecipe(), "package.zip"); err == nil {
			t.Errorf("linked destination accepted: %s", dest)
		}
	}
	contents, err := os.ReadDir(outside)
	if err != nil || len(contents) != 0 {
		t.Fatalf("outside modified: %v %v", contents, err)
	}
	occupied := t.TempDir()
	if err := os.WriteFile(filepath.Join(occupied, "original"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := PreparePackage(archive, occupied, packageRecipe(), "package.zip"); err == nil {
		t.Fatal("nonempty destination accepted")
	}
}

func TestPreparePackageRejectsOversizeAndCorruptArchives(t *testing.T) {
	p := packageRecipe()
	p.Format = "tar.gz"
	p.Mappings = []Mapping{{From: ".", To: "addons"}}
	archive := packageTar(t, []packageEntry{{name: "huge", size: 65 << 20}})
	if _, err := PreparePackage(archive, filepath.Join(t.TempDir(), "stage"), p, "package.tar.gz"); err == nil {
		t.Fatal("oversize member accepted")
	}
	archive = packageTar(t, []packageEntry{{name: "file", body: "hello"}})
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-8] ^= 0xff
	if err = os.WriteFile(archive, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := PreparePackage(archive, filepath.Join(t.TempDir(), "stage"), p, "package.tar.gz"); err == nil {
		t.Fatal("corrupt gzip accepted")
	}
}

func TestPreparePackageBoundsMemberCount(t *testing.T) {
	entries := make([]packageEntry, 10001)
	for i := range entries {
		entries[i] = packageEntry{name: strings.Repeat("a", i/1000+1) + "/" + string(rune(0x1000+i)), body: ""}
	}
	archive := packageZIP(t, entries)
	p := packageRecipe()
	p.Mappings = []Mapping{{From: ".", To: "addons"}}
	if _, err := PreparePackage(archive, filepath.Join(t.TempDir(), "stage"), p, "package.zip"); err == nil {
		t.Fatal("archive with excessive member count accepted")
	}
}

func TestPreparePackageRejectsDirectoryFileConflict(t *testing.T) {
	archive := packageZIP(t, []packageEntry{{name: "release/addons/file", body: "one"}, {name: "release/addons/file/nested", body: "two"}})
	if _, err := PreparePackage(archive, filepath.Join(t.TempDir(), "stage"), packageRecipe(), "package.zip"); err == nil {
		t.Fatal("archive file-directory conflict accepted")
	}
}

func TestPreparePackageBoundsZIPMetadata(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "metadata.zip")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	// File payloads are empty, but untrusted comments alone exceed the metadata
	// budget. Reject before archive/zip allocates every central-directory entry.
	for i := 0; i < 150; i++ {
		_, err = writer.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("addons/file-%d", i), Comment: strings.Repeat("x", 60000)})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	p := packageRecipe()
	p.Mappings = []Mapping{{From: ".", To: "."}}
	if _, err := PreparePackage(filename, filepath.Join(t.TempDir(), "stage"), p, "metadata.zip"); err == nil {
		t.Fatal("excessive ZIP metadata accepted")
	}
}
