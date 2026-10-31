package plugins

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

var compilerTag = regexp.MustCompile(`^1\.[0-9]+\.[0-9]+\.[0-9]+$`)

func validateBuild(b Build) error {
	if !compilerTag.MatchString(b.SourceMod) {
		return errors.New("build.sourcemod must be an exact SourceMod release tag, such as 1.13.0.7478")
	}
	if len(b.Sources) == 0 || len(b.Sources) > 100 {
		return errors.New("build.sources requires 1–100 SourcePawn paths or patterns")
	}
	for _, pattern := range b.Sources {
		if !cleanRelative(pattern, false) || !strings.HasSuffix(pattern, ".sp") {
			return errors.New("build.sources must contain relative .sp paths or patterns")
		}
		if _, err := path.Match(pattern, "probe"); err != nil {
			return errors.New("invalid SourcePawn source pattern")
		}
	}
	if err := validateIncludeDirs(b.IncludeDirs, false); err != nil {
		return err
	}
	if len(b.Dependencies) > 20 {
		return errors.New("a build supports at most 20 include dependencies")
	}
	for _, dep := range b.Dependencies {
		if err := validateSource(dep.Source); err != nil {
			return fmt.Errorf("build dependency: %w", err)
		}
		if err := validateIncludeDirs(dep.IncludeDirs, true); err != nil {
			return fmt.Errorf("build dependency: %w", err)
		}
	}
	return nil
}

func validateIncludeDirs(dirs []string, required bool) error {
	if len(dirs) > 100 || (required && len(dirs) == 0) {
		return errors.New("include_dirs supports up to 100 paths and requires at least one for dependencies")
	}
	for _, dir := range dirs {
		if !cleanRelative(dir, true) || strings.ContainsAny(dir, "*?[") {
			return errors.New("include_dirs must contain relative directory paths")
		}
	}
	return nil
}
