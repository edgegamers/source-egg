package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CompilerProvider returns a trusted SourceMod scripting directory. Repository
// content is never used as an executable or a build script.
type CompilerProvider func(context.Context, string) (string, error)

type SourceBuilder struct {
	CacheDir string
	Source   ReleaseSource
	Compiler CompilerProvider
}

func (b SourceBuilder) Build(ctx context.Context, p Plugin, r Release, archive, dest string) (map[string]File, error) {
	if err := ValidatePlugin(p); err != nil {
		return nil, err
	}
	if p.Build == nil || len(r.Dependencies) != len(p.Build.Dependencies) {
		return nil, errors.New("source build requires a recipe and its resolved include revisions")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	parent, name, err := acquisitionParent(dest)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	if err := packageEmptyDestination(parent, name); err != nil {
		return nil, err
	}
	work, err := os.MkdirTemp(filepath.Dir(dest), "source-build-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	extracted, repository, err := extractRepository(archive, filepath.Join(work, "repository"))
	if err != nil {
		return nil, err
	}
	defer extracted.root.Close()
	if _, err := extracted.root.Lstat("compiled"); !os.IsNotExist(err) {
		return nil, errors.New("repository contains the reserved compiled path")
	}
	includes, err := buildIncludePaths(repository, p.Build.IncludeDirs)
	if err != nil {
		return nil, err
	}
	for index, dep := range p.Build.Dependencies {
		if b.Source == nil {
			return nil, errors.New("include dependency downloader is unavailable")
		}
		depArchive := filepath.Join(work, fmt.Sprintf("dependency-%d.tar.gz", index))
		depRecipe := p
		depRecipe.Source = dep.Source
		if err := b.Source.Download(ctx, depRecipe, Release{Asset: r.Dependencies[index].Asset}, depArchive); err != nil {
			return nil, fmt.Errorf("include dependency download: %w", err)
		}
		depFiles, depDir, err := extractRepository(depArchive, filepath.Join(work, fmt.Sprintf("dependency-%d", index)))
		if err != nil {
			return nil, fmt.Errorf("include dependency archive: %w", err)
		}
		depIncludes, includeErr := buildIncludePaths(depDir, dep.IncludeDirs)
		depFiles.root.Close()
		if includeErr != nil {
			return nil, includeErr
		}
		includes = append(includes, depIncludes...)
	}
	sources, err := buildSourcePaths(repository, p.Build.Sources)
	if err != nil {
		return nil, err
	}
	var scripting string
	if b.Compiler != nil {
		scripting, err = b.Compiler(ctx, p.Build.SourceMod)
	} else {
		scripting, err = b.compilerDirectory(ctx, p.Build.SourceMod, work)
	}
	if err != nil {
		return nil, fmt.Errorf("SourceMod compiler: %w", err)
	}
	compiler, err := buildCompilerPath(scripting)
	if err != nil {
		return nil, err
	}
	standardIncludes, err := buildIncludePaths(scripting, []string{"include"})
	if err != nil {
		return nil, err
	}
	includes = append(includes, standardIncludes...)
	if err := extracted.root.Mkdir("compiled", 0700); err != nil {
		return nil, err
	}
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		output := "compiled/" + strings.TrimSuffix(filepath.Base(source), ".sp") + ".smx"
		args := []string{"-O2", "-v2", "-o" + filepath.Join(repository, filepath.FromSlash(output))}
		for _, include := range append([]string{filepath.Dir(source)}, includes...) {
			args = append(args, "-i"+include)
		}
		args = append(args, source)
		compileCtx, stop := context.WithTimeout(ctx, time.Minute)
		cmd := exec.CommandContext(compileCtx, compiler, args...)
		cmd.Dir = repository
		cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "HOME=" + work, "TMPDIR=" + work}
		diagnostics := &buildDiagnostics{}
		cmd.Stdout, cmd.Stderr = diagnostics, diagnostics
		err := cmd.Run()
		contextErr := compileCtx.Err()
		stop()
		if contextErr != nil {
			return nil, fmt.Errorf("compile %s: %w", filepath.Base(source), contextErr)
		}
		if err != nil {
			return nil, fmt.Errorf("compile %s failed: %s: %s", filepath.Base(source), err, b.redact(diagnostics.String(), diagnostics.truncated))
		}
		member, size, err := compiledMember(extracted.root, output)
		if err != nil {
			return nil, err
		}
		if err := extracted.count(); err != nil || extracted.total > maxPackageExpanded-size {
			return nil, errors.New("compiled package exceeds archive limits")
		}
		extracted.total += size
		extracted.members[output] = member
	}
	return extracted.publish(parent, name, p)
}

func extractRepository(archive, dest string) (*packageExtraction, string, error) {
	parent, name, err := acquisitionParent(archive)
	if err != nil {
		return nil, "", err
	}
	defer parent.Close()
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() || info.Size() > maxPluginDownload {
		return nil, "", errors.New("source archive must be a regular file of at most 256 MiB")
	}
	file, err := parent.Open(name)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	if err := os.Mkdir(dest, 0700); err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return nil, "", err
	}
	defer root.Close()
	extracted := &packageExtraction{root: root, members: map[string]packageMember{}}
	if err := extracted.tar(file); err != nil {
		return nil, "", err
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		return nil, "", err
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return nil, "", errors.New("source archive must contain exactly one repository wrapper directory")
	}
	wrapper := entries[0].Name()
	members := map[string]packageMember{}
	for name, member := range extracted.members {
		rel, ok := strings.CutPrefix(name, wrapper+"/")
		if !ok || !cleanRelative(rel, false) {
			return nil, "", errors.New("invalid repository wrapper layout")
		}
		members[rel] = member
	}
	if len(members) == 0 {
		return nil, "", errors.New("repository archive is empty")
	}
	repositoryRoot, err := root.OpenRoot(wrapper)
	if err != nil {
		return nil, "", err
	}
	extracted.root, extracted.members = repositoryRoot, members
	return extracted, filepath.Join(dest, wrapper), nil
}

func buildIncludePaths(root string, dirs []string) ([]string, error) {
	var includes []string
	for _, dir := range dirs {
		name := filepath.Join(root, filepath.FromSlash(dir))
		info, err := os.Lstat(name)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("include directory is missing or invalid: %s", dir)
		}
		includes = append(includes, name)
	}
	return includes, nil
}

func buildSourcePaths(root string, patterns []string) ([]string, error) {
	var sources []string
	outputs := map[string]bool{}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil || len(matches) == 0 {
			return nil, fmt.Errorf("source pattern has no files: %s", pattern)
		}
		for _, name := range matches {
			info, err := os.Lstat(name)
			if err != nil || !info.Mode().IsRegular() {
				return nil, errors.New("SourcePawn input must be a regular file")
			}
			basename := filepath.Base(name)
			if outputs[basename] {
				return nil, fmt.Errorf("duplicate compiled output basename: %s", basename)
			}
			outputs[basename] = true
			sources = append(sources, name)
			if len(sources) > 100 {
				return nil, errors.New("source patterns select more than 100 files")
			}
		}
	}
	sort.Strings(sources)
	return sources, nil
}

func buildCompilerPath(scripting string) (string, error) {
	for _, name := range []string{"spcomp64", "spcomp"} {
		filename := filepath.Join(scripting, name)
		info, err := os.Lstat(filename)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return "", errors.New("trusted SourceMod compiler is not a regular executable")
		}
		return filename, nil
	}
	return "", errors.New("pinned SourceMod archive contains no supported Linux compiler")
}

func compiledMember(root *os.Root, name string) (packageMember, int64, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return packageMember{}, 0, errors.New("compiler did not produce the expected .smx file")
	}
	if !info.Mode().IsRegular() || info.Size() < 4 || info.Size() > maxPackageFile {
		return packageMember{}, 0, errors.New("compiled .smx must be a regular SourcePawn binary of at most 64 MiB")
	}
	file, err := root.Open(name)
	if err != nil {
		return packageMember{}, 0, err
	}
	defer file.Close()
	var magic [4]byte
	if _, err := io.ReadFull(file, magic[:]); err != nil || string(magic[:]) != "FFPS" {
		return packageMember{}, 0, errors.New("compiler produced an invalid SourcePawn binary")
	}
	hash := sha256.New()
	hash.Write(magic[:])
	if _, err := io.Copy(hash, io.LimitReader(file, maxPackageFile)); err != nil {
		return packageMember{}, 0, err
	}
	return packageMember{digest: hex.EncodeToString(hash.Sum(nil)), mode: 0644}, info.Size(), nil
}

type buildDiagnostics struct {
	data      strings.Builder
	truncated bool
}

func (d *buildDiagnostics) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 8192 - d.data.Len()
	if n > remaining {
		d.truncated = true
	}
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		d.data.Write(p)
	}
	return n, nil
}
func (d *buildDiagnostics) String() string { return d.data.String() }

func (b SourceBuilder) redact(output string, truncated bool) string {
	var credentials map[string]string
	switch source := b.Source.(type) {
	case HTTPSource:
		credentials = source.Credentials
	case *HTTPSource:
		credentials = source.Credentials
	}
	secrets := make([]string, 0, len(credentials))
	for _, secret := range credentials {
		if secret != "" {
			secrets = append(secrets, secret)
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	if truncated {
		// Capture can end in the middle of a credential. Remove that partial suffix
		// before exact replacements, including when output was split across writes.
		longest := 0
		for _, secret := range secrets {
			for n := min(len(secret), len(output)); n > longest; n-- {
				if strings.HasSuffix(output, secret[:n]) {
					longest = n
					break
				}
			}
		}
		if longest > 0 {
			output = output[:len(output)-longest] + "[redacted]"
		}
	}
	for _, secret := range secrets {
		output = strings.ReplaceAll(output, secret, "[redacted]")
	}
	if len(output) > 8192 {
		output = output[:8192]
	}
	return strings.TrimSpace(output)
}
