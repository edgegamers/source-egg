package content

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/edgegamers/source-egg/internal/game"
)

type Steam struct {
	Executable, Username, Password string
	Validate                       bool
	Output                         io.Writer
	mu                             sync.Mutex
}

func (s *Steam) Install(ctx context.Context, id game.Identity, dir string) error {
	if e := id.Validate(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := os.MkdirAll(dir, 0755); e != nil {
		return e
	}
	abs, e := filepath.Abs(dir)
	if e != nil {
		return e
	}
	args := []string{"+force_install_dir", abs, "+login"}
	if s.Username == "" {
		args = append(args, "anonymous")
	} else {
		args = append(args, s.Username, s.Password)
	}
	args = append(args, "+app_update", id.AppID)
	if id.Branch != "public" {
		args = append(args, "-beta", id.Branch)
	}
	if s.Validate {
		args = append(args, "validate")
	}
	args = append(args, "+quit")
	cmd := exec.CommandContext(ctx, s.Executable, args...)
	cmd.Stdout = s.Output
	cmd.Stderr = s.Output
	if e = cmd.Run(); e != nil {
		return fmt.Errorf("SteamCMD failed for %s: %w", id.Game, e)
	}
	_, e = InstalledBuild(abs, id)
	return e
}

var buildRE = regexp.MustCompile(`"buildid"\s+"([0-9]+)"`)

func InstalledBuild(dir string, id game.Identity) (string, error) {
	if e := id.Validate(); e != nil {
		return "", e
	}
	p, _ := game.Lookup(id.Game)
	root, e := os.OpenRoot(dir)
	if e != nil {
		return "", e
	}
	defer root.Close()
	for _, name := range []string{strings.TrimPrefix(p.Executable, "./"), p.Dir + "/gameinfo.txt"} {
		if _, e = root.Stat(name); e != nil {
			return "", fmt.Errorf("incomplete %s installation: missing %s", id.Game, name)
		}
	}
	data, e := root.ReadFile("steamapps/appmanifest_" + id.AppID + ".acf")
	if e != nil {
		return "", e
	}
	text := string(data)
	appRE := regexp.MustCompile(`"appid"\s+"` + regexp.QuoteMeta(id.AppID) + `"`)
	if !appRE.MatchString(text) {
		return "", errors.New("Steam manifest app mismatch")
	}
	if !regexp.MustCompile(`"StateFlags"\s+"4"`).MatchString(text) {
		return "", errors.New("Steam install not fully installed")
	}
	m := buildRE.FindStringSubmatch(text)
	if len(m) != 2 {
		return "", errors.New("Steam manifest has no build ID")
	}
	return m[1], nil
}

// RemoteBuild is an optimization only. Failure must not trigger a rollout.
func (s *Steam) RemoteBuild(ctx context.Context, id game.Identity) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	args := []string{"+login", "anonymous", "+app_info_update", "1", "+app_info_print", id.AppID, "+quit"}
	if s.Username != "" {
		args = []string{"+login", s.Username, s.Password, "+app_info_update", "1", "+app_info_print", id.AppID, "+quit"}
	}
	b, e := exec.CommandContext(ctx, s.Executable, args...).Output()
	if e != nil {
		return "", errors.New("Steam build lookup failed")
	}
	start := strings.Index(string(b), `"`+id.AppID+`"`)
	if start < 0 {
		return "", errors.New("Steam app info missing")
	}
	tokens := regexp.MustCompile(`"([^"\r\n]*)"|[{}]`).FindAllString(string(b[start:]), -1)
	i := 0
	tree, e := parseKV(tokens, &i)
	if e != nil {
		return "", e
	}
	for _, key := range []string{id.AppID, "depots", "branches", id.Branch} {
		v, ok := tree[key].(map[string]any)
		if !ok {
			return "", errors.New("Steam branch metadata missing")
		}
		tree = v
	}
	value, ok := tree["buildid"].(string)
	if !ok || !regexp.MustCompile(`^[0-9]+$`).MatchString(value) {
		return "", errors.New("invalid remote build")
	}
	return value, nil
}
func parseKV(tokens []string, index *int) (map[string]any, error) {
	result := map[string]any{}
	for *index < len(tokens) {
		key := tokens[*index]
		*index++
		if key == "}" {
			return result, nil
		}
		if key == "{" || *index >= len(tokens) {
			return nil, errors.New("invalid Steam metadata")
		}
		value := tokens[*index]
		*index++
		if value == "{" {
			nested, e := parseKV(tokens, index)
			if e != nil {
				return nil, e
			}
			result[strings.Trim(key, `"`)] = nested
		} else {
			result[strings.Trim(key, `"`)] = strings.Trim(value, `"`)
		}
	}
	return result, nil
}
