package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A copy of this executable acts as a compiler so command construction and
// process cancellation are exercised without shell scripts or network access.
func init() {
	if filepath.Base(os.Args[0]) != "spcomp64" {
		return
	}
	source := os.Args[len(os.Args)-1]
	data, _ := os.ReadFile(source)
	if strings.Contains(string(data), "SLEEP") {
		time.Sleep(time.Minute)
	}
	if strings.Contains(string(data), "FAIL") {
		fmt.Print(strings.Repeat("diagnostic ", 10000) + "private-secret")
		os.Exit(1)
	}
	if strings.Contains(string(data), "SECRET") {
		fmt.Print("private-secret")
		os.Exit(1)
	}
	if strings.Contains(string(data), "BOUNDARY") {
		fmt.Print(strings.Repeat(" ", 8192-len("private-")) + "private-secret")
		os.Exit(1)
	}
	var output string
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "-o") {
			output = strings.TrimPrefix(arg, "-o")
		}
	}
	magic := "FFPS"
	if strings.Contains(string(data), "BAD") {
		magic = "BAD!"
	}
	if strings.Contains(string(data), "EMPTY") {
		magic = ""
	}
	for _, env := range os.Environ() {
		key := strings.SplitN(env, "=", 2)[0]
		if key != "PATH" && key != "HOME" && key != "TMPDIR" && key != "LANG" {
			fmt.Fprintln(os.Stderr, "unexpected inherited environment:", key)
			os.Exit(2)
		}
	}
	body := magic + "\n" + strings.Join(os.Args[1:], "\n")
	if strings.Contains(string(data), "STABLE") {
		body = magic + "stable compiled bytes"
	}
	if err := os.WriteFile(output, []byte(body), 0600); err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

type buildFixtureSource struct {
	archive string
}

func (s buildFixtureSource) Resolve(context.Context, Plugin) (Release, error) {
	return Release{}, errors.New("fixture only downloads")
}
func (s buildFixtureSource) Download(_ context.Context, _ Plugin, _ Release, dest string) error {
	data, err := os.ReadFile(s.archive)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0600)
}

func fixtureCompiler(t *testing.T) CompilerProvider {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "include"), 0700); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(filepath.Join(dir, "spcomp64"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(out, in)
	out.Close()
	if err != nil {
		t.Fatal(err)
	}
	return func(ctx context.Context, tag string) (string, error) {
		if tag != "1.13.0.7478" {
			return "", errors.New("unexpected compiler pin")
		}
		return dir, ctx.Err()
	}
}

func buildRecipe(t *testing.T) Plugin {
	t.Helper()
	p := sourceRecipe(t)
	p.Build.Dependencies = nil
	p.Mappings = []Mapping{{From: "compiled/demo.smx", To: "addons/sourcemod/plugins/disabled/renamed.smx"}, {From: "gamedata", To: "addons/sourcemod/gamedata"}}
	return p
}

func TestSourceBuilderCompilesMapsAndIncludes(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "must-not-be-inherited")
	p := buildRecipe(t)
	p.Build.Dependencies = sourceRecipe(t).Build.Dependencies
	archive := packageTar(t, []packageEntry{{name: "repo-hash/scripting/demo.sp", body: "OK"}, {name: "repo-hash/scripting/include/local.inc", body: "local include"}, {name: "repo-hash/gamedata/demo.txt", body: "offsets"}})
	dep := packageTar(t, []packageEntry{{name: "dependency-hash/addons/sourcemod/scripting/include/colors.inc", body: "colors"}})
	r := Release{Dependencies: []Revision{{Commit: strings.Repeat("b", 40), Asset: Asset{Name: "source.tar.gz"}}}}
	b := SourceBuilder{CacheDir: t.TempDir(), Compiler: fixtureCompiler(t), Source: buildFixtureSource{archive: dep}}
	dest := filepath.Join(t.TempDir(), "payload")
	files, err := b.Build(context.Background(), p, r, archive, dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files["addons/sourcemod/gamedata/demo.txt"].SHA256 == "" {
		t.Fatalf("bad mapped files: %#v", files)
	}
	data, err := os.ReadFile(filepath.Join(dest, "addons/sourcemod/plugins/disabled/renamed.smx"))
	if err != nil || !strings.HasPrefix(string(data), "FFPS") {
		t.Fatalf("compiled output: %q %v", data, err)
	}
	for _, wanted := range []string{"scripting/include", "scripting/demo.sp", "scripting\n", "addons/sourcemod/scripting/include", "-O2", "-v2"} {
		if !strings.Contains(string(data), wanted) {
			t.Errorf("compiler did not receive %q: %s", wanted, data)
		}
	}
}

func TestSourceBuilderRejectsInvalidInputsAndOutputs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []packageEntry
		mutate  func(*Plugin)
	}{
		{name: "missing sources", entries: []packageEntry{{name: "repo/gamedata/demo.txt", body: "offset"}}},
		{name: "two wrappers", entries: []packageEntry{{name: "one/demo.sp", body: "ok"}, {name: "two/demo.sp", body: "ok"}}},
		{name: "no wrapper", entries: []packageEntry{{name: "demo.sp", body: "ok"}}},
		{name: "reserved outputs", entries: []packageEntry{{name: "repo/compiled/evil.smx", body: "prebuilt"}}},
		{name: "duplicate basename", entries: []packageEntry{{name: "repo/a/demo.sp", body: "ok"}, {name: "repo/b/demo.sp", body: "ok"}}, mutate: func(p *Plugin) { p.Build.Sources = []string{"a/*.sp", "b/*.sp"} }},
		{name: "compiler failure", entries: []packageEntry{{name: "repo/scripting/demo.sp", body: "FAIL"}}},
		{name: "invalid binary", entries: []packageEntry{{name: "repo/scripting/demo.sp", body: "BAD"}}},
		{name: "missing include", entries: []packageEntry{{name: "repo/scripting/demo.sp", body: "OK"}}, mutate: func(p *Plugin) { p.Build.IncludeDirs = []string{"missing"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := buildRecipe(t)
			p.Build.IncludeDirs = nil
			p.Mappings = p.Mappings[:1]
			if tc.mutate != nil {
				tc.mutate(&p)
			}
			b := SourceBuilder{Compiler: fixtureCompiler(t)}
			dest := filepath.Join(t.TempDir(), "payload")
			_, err := b.Build(context.Background(), p, Release{}, packageTar(t, tc.entries), dest)
			if err == nil {
				t.Fatal("accepted invalid build")
			}
			if len(err.Error()) > 9000 {
				t.Fatalf("unbounded diagnostics: %d bytes", len(err.Error()))
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatal("failed build published a payload")
			}
		})
	}
}

func TestSourceBuilderCancellationAndCredentialRedaction(t *testing.T) {
	p := buildRecipe(t)
	p.Build.IncludeDirs = nil
	p.Mappings = p.Mappings[:1]
	b := SourceBuilder{Compiler: fixtureCompiler(t), Source: &HTTPSource{Credentials: map[string]string{"gitlab": "private-secret"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := b.Build(ctx, p, Release{}, packageTar(t, []packageEntry{{name: "repo/scripting/demo.sp", body: "SLEEP"}}), filepath.Join(t.TempDir(), "payload"))
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation: %v", err)
	}
	_, err = b.Build(context.Background(), p, Release{}, packageTar(t, []packageEntry{{name: "repo/scripting/demo.sp", body: "SECRET"}}), filepath.Join(t.TempDir(), "payload"))
	if err == nil || strings.Contains(err.Error(), "private-secret") || !strings.Contains(err.Error(), "redacted") {
		t.Fatalf("credential diagnostic: %v", err)
	}
}

func TestSourceBuilderRealCompiler(t *testing.T) {
	tag := os.Getenv("SOURCE_EGG_TEST_COMPILER_RELEASE")
	if tag == "" {
		t.Skip("set SOURCE_EGG_TEST_COMPILER_RELEASE to exercise the public compiler download")
	}
	p := buildRecipe(t)
	p.Build.SourceMod, p.Build.IncludeDirs = tag, nil
	p.Mappings = p.Mappings[:1]
	archive := packageTar(t, []packageEntry{{name: "repository/scripting/demo.sp", body: `#include <sourcemod>
public Plugin myinfo = { name = "SourceEgg compiler check", author = "SourceEgg", description = "Smoke check", version = "1.0", url = "" };
public void OnPluginStart() { PrintToServer("SourceEgg compiled this plugin"); }
`}})
	b := SourceBuilder{CacheDir: t.TempDir()}
	files, err := b.Build(context.Background(), p, Release{}, archive, filepath.Join(t.TempDir(), "payload"))
	if err != nil || len(files) != 1 {
		t.Fatalf("real SourceMod compiler: %v, %v", files, err)
	}
}

func TestSourceBuilderTruncatedCredentialRedaction(t *testing.T) {
	p := buildRecipe(t)
	p.Build.IncludeDirs, p.Mappings = nil, p.Mappings[:1]
	b := SourceBuilder{Compiler: fixtureCompiler(t), Source: &HTTPSource{Credentials: map[string]string{"gitlab": "private-secret"}}}
	_, err := b.Build(context.Background(), p, Release{}, packageTar(t, []packageEntry{{name: "repo/scripting/demo.sp", body: "BOUNDARY"}}), filepath.Join(t.TempDir(), "payload"))
	if err == nil || strings.Contains(err.Error(), "private-") {
		t.Fatalf("truncated credential prefix leaked: %v", err)
	}
	b.Source = &HTTPSource{Credentials: map[string]string{"short": "a"}}
	if diagnostic := b.redact(strings.Repeat("a", 8192), false); len(diagnostic) > 8192 {
		t.Fatalf("redaction expanded diagnostics beyond their limit: %d", len(diagnostic))
	}
	for _, tc := range []struct {
		values        map[string]string
		input, leaked string
	}{
		{map[string]string{"overlap": "abcabc"}, "diagnostic abcabc", "abc"},
		{map[string]string{"one": "abcdefgh", "two": "xyzabc"}, "diagnostic xyzabc", "xyz"},
	} {
		b.Source = &HTTPSource{Credentials: tc.values}
		if diagnostic := b.redact(tc.input, true); strings.Contains(diagnostic, tc.leaked) {
			t.Fatalf("overlapping credential suffix leaked: %s", diagnostic)
		}
	}
}

func TestSourceBuilderRejectsSymlinkToolchainCache(t *testing.T) {
	outside := t.TempDir()
	cache := filepath.Join(t.TempDir(), "toolchains")
	if err := os.Symlink(outside, cache); err != nil {
		t.Fatal(err)
	}
	b := SourceBuilder{CacheDir: cache}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := b.compilerDirectory(ctx, "1.13.0.7478", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("cache symlink was not rejected: %v", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("toolchain cache changed an outside directory: %v %v", entries, err)
	}
}

func TestCompilerCacheVerifiedArchiveAndSymlinkEntry(t *testing.T) {
	archive := packageTar(t, []packageEntry{{name: "addons/sourcemod/scripting/spcomp64", body: "trusted fixture compiler", mode: 0755}, {name: "addons/sourcemod/scripting/include/sourcemod.inc", body: "fixture include"}})
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	cache := t.TempDir()
	name := "sourcemod-1.13.0.7478"
	receipt, _ := json.Marshal(compilerReceipt{Tag: "1.13.0.7478", SHA256: hex.EncodeToString(hash[:])})
	if err := os.WriteFile(filepath.Join(cache, name+".json"), receipt, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, name+".tar.gz"), data, 0600); err != nil {
		t.Fatal(err)
	}
	b := SourceBuilder{CacheDir: cache}
	dir, err := b.compilerDirectory(context.Background(), "1.13.0.7478", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "spcomp64")); err != nil || string(data) != "trusted fixture compiler" {
		t.Fatalf("cached compiler extraction: %q %v", data, err)
	}
	// Each cache entry, as well as the cache root, must reject links.
	if err := os.Remove(filepath.Join(cache, name+".tar.gz")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(archive, filepath.Join(cache, name+".tar.gz")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.compilerDirectory(context.Background(), "1.13.0.7478", t.TempDir()); err == nil {
		t.Fatal("toolchain cache followed an archive symlink")
	}
	if after, _ := os.ReadFile(archive); string(after) != string(data) {
		t.Fatal("toolchain cache modified an external file")
	}
}
