package addons

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ValidatePluginOptions(options map[string]string) error {
	known := map[string]bool{}
	for _, name := range StockPlugins() {
		known[name] = true
	}
	for name, value := range options {
		if !known[name] {
			return fmt.Errorf("unknown stock SourceMod plugin %q", name)
		}
		if value != "" && value != "default" && value != "0" && value != "1" {
			return fmt.Errorf("SourceMod plugin %s must be default, 1 or 0", name)
		}
	}
	return nil
}

func pluginTarget(root *os.Root, canonical, previous string, owned bool, policy string) (string, error) {
	other := alternatePluginPath(canonical)
	for _, name := range []string{canonical, other} {
		if err := stockPathNoLinks(root, name); err != nil {
			return "", err
		}
	}
	sum, err := rootDigest(root, canonical)
	otherSum, otherErr := rootDigest(root, other)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if otherErr != nil && !os.IsNotExist(otherErr) {
		return "", otherErr
	}
	if err == nil && otherErr == nil {
		return "", fmt.Errorf("duplicate active/disabled plugin: %s", filepath.Base(canonical))
	}
	current := canonical
	exists := err == nil
	if otherErr == nil {
		current, sum, exists = other, otherSum, true
	}
	if policy != "0" && policy != "1" {
		return current, nil
	}
	want := canonical
	disabled := strings.HasSuffix(filepath.Dir(canonical), "/plugins/disabled")
	if (policy == "0" && !disabled) || (policy == "1" && disabled) {
		want = other
	}
	if exists && current != want {
		if !owned || sum != previous {
			return "", fmt.Errorf("refusing to move custom SourceMod plugin %s", filepath.Base(canonical))
		}
		if err := root.MkdirAll(filepath.Dir(want), 0755); err != nil {
			return "", err
		}
		if err := root.Rename(current, want); err != nil {
			return "", err
		}
	}
	return want, nil
}

func stockPathNoLinks(root *os.Root, name string) error {
	cur := ""
	for _, part := range strings.Split(name, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		st, err := root.Lstat(cur)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return errors.New("stock file path contains a symlink")
		}
	}
	return nil
}

// StockPlugins are SourceMod equivalents of the applicable upstream switches.
func StockPlugins() []string {
	return []string{"basecommands", "basechat", "playercommands", "antiflood", "adminhelp", "basecomm", "basebans", "reservedslots", "basetriggers", "funcommands", "clientprefs", "adminmenu", "basevotes", "nominations", "rockthevote", "nextmap", "funvotes", "mapchooser"}
}

func alternatePluginPath(name string) string {
	base := filepath.Base(name)
	if !strings.HasSuffix(base, ".smx") {
		return ""
	}
	dir := filepath.Dir(name)
	if strings.HasSuffix(dir, "/addons/sourcemod/plugins") {
		return filepath.Join(dir, "disabled", base)
	}
	if strings.HasSuffix(dir, "/addons/sourcemod/plugins/disabled") {
		return filepath.Join(filepath.Dir(dir), base)
	}
	return ""
}
