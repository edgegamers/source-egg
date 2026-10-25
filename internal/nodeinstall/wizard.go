package nodeinstall

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/edgegamers/source-egg/internal/game"
	"github.com/edgegamers/source-egg/internal/node"
)

type wizard struct {
	input  *bufio.Reader
	output io.Writer
}

func (w wizard) ask(label, def string, valid func(string) error) (string, error) {
	for {
		fmt.Fprintf(w.output, "%s [%s]: ", label, def)
		line, err := w.input.ReadString('\n')
		if err != nil && len(line) == 0 {
			return "", fmt.Errorf("setup interrupted before saving: %w", err)
		}
		value := strings.TrimSpace(line)
		if value == "" {
			value = def
		}
		if valid != nil {
			if err = valid(value); err != nil {
				fmt.Fprintln(w.output, " ", err)
				continue
			}
		}
		return value, nil
	}
}
func (w wizard) boolean(label string, def bool) (bool, error) {
	value := "no"
	if def {
		value = "yes"
	}
	got, err := w.ask(label, value, func(s string) error {
		switch strings.ToLower(s) {
		case "yes", "no", "y", "n":
			return nil
		}
		return fmt.Errorf("enter yes or no")
	})
	return strings.EqualFold(got, "yes") || strings.EqualFold(got, "y"), err
}
func (w wizard) number(label string, def, min, max int) (int, error) {
	value, err := w.ask(label, strconv.Itoa(def), func(s string) error {
		n, err := strconv.Atoi(s)
		if err != nil || n < min || n > max {
			return fmt.Errorf("enter a number from %d to %d", min, max)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	n, _ := strconv.Atoi(value)
	return n, nil
}
func (w wizard) hook(label, current string) (string, error) {
	display := "disabled"
	if current != "" {
		display = "configured"
	}
	for {
		fmt.Fprintf(w.output, "%s [%s; Enter keeps, - clears]: ", label, display)
		line, err := w.input.ReadString('\n')
		if err != nil && len(line) == 0 {
			return "", fmt.Errorf("setup interrupted before saving: %w", err)
		}
		value := strings.TrimSpace(line)
		if value == "" {
			return current, nil
		}
		if value == "-" {
			return "", nil
		}
		if strings.HasPrefix(value, "https://discord.com/api/webhooks/") {
			return value, nil
		}
		fmt.Fprintln(w.output, "Use a Discord HTTPS webhook URL, or - to disable.")
	}
}
func games(s string) ([]string, error) {
	if s == "none" || s == "-" || s == "" {
		return nil, nil
	}
	var result []string
	seen := map[string]bool{}
	for _, key := range strings.Split(s, ",") {
		p, err := game.Lookup(strings.TrimSpace(key))
		if err != nil {
			return nil, fmt.Errorf("use comma-separated csgo,css,dods,l4d2,tf2 or none")
		}
		if !seen[p.Key] {
			seen[p.Key] = true
			result = append(result, p.Key)
		}
	}
	return result, nil
}

// Configure gathers all answers before the installer writes anything.
func Configure(c node.Config, input io.Reader, output io.Writer) (node.Config, error) {
	w := wizard{bufio.NewReader(input), output}
	var err error
	fmt.Fprintln(output, "Source Egg node setup\nInstall on the Wings node. No game servers need to exist yet.\nPress Enter to keep each current value.")
	if c.Shared, err = w.boolean("Share base-game VPK files", c.Shared); err != nil {
		return c, err
	}
	if c.Shared {
		if c.SharedDir, err = w.ask("Shared game directory", c.SharedDir, nil); err != nil {
			return c, err
		}
		list := strings.Join(c.PrefetchGames, ",")
		if list == "" {
			list = "none"
		}
		answer, e := w.ask("Games to prepare now (none downloads on first server start)", list, func(s string) error { _, err := games(s); return err })
		if e != nil {
			return c, e
		}
		c.PrefetchGames, _ = games(answer)
	}
	if c.FrameworkCache, err = w.boolean("Cache Metamod/SourceMod downloads", c.FrameworkCache); err != nil {
		return c, err
	}
	if c.FrameworkCache {
		if c.CacheDir, err = w.ask("Framework cache directory", c.CacheDir, nil); err != nil {
			return c, err
		}
	}
	if c.Restart.Mode, err = w.ask("Game-update restart policy (immediate, empty, window)", c.Restart.Mode, func(s string) error {
		if s != "immediate" && s != "empty" && s != "window" {
			return fmt.Errorf("choose immediate, empty or window")
		}
		return nil
	}); err != nil {
		return c, err
	}
	if c.Restart.Mode == "window" {
		if c.Restart.Window == "" {
			c.Restart.Window = "03:00-05:00"
		}
		if c.Restart.Window, err = w.ask("Restart window in node local time", c.Restart.Window, func(s string) error { copy := c; copy.Restart.Window = s; return copy.Validate() }); err != nil {
			return c, err
		}
	}
	if c.Restart.MaxDelayMinutes, err = w.number("Maximum restart delay minutes (0 never forces)", c.Restart.MaxDelayMinutes, 0, 10080); err != nil {
		return c, err
	}
	if c.Restart.WarnSeconds, err = w.number("Restart countdown seconds", c.Restart.WarnSeconds, 0, 3600); err != nil {
		return c, err
	}
	if c.CheckMinutes, err = w.number("Game update check interval minutes", c.CheckMinutes, 1, 1440); err != nil {
		return c, err
	}
	if c.PasswordScope, err = w.ask("Dev join-password sharing (game, server, node)", c.PasswordScope, func(s string) error {
		if s != "game" && s != "server" && s != "node" {
			return fmt.Errorf("choose game, server or node")
		}
		return nil
	}); err != nil {
		return c, err
	}
	if c.PasswordIntervalHours, err = w.number("Join-password rotation hours (0 disables expiry)", c.PasswordIntervalHours, 0, 8760); err != nil {
		return c, err
	}
	if c.Webhook, err = w.hook("Discord event webhook", c.Webhook); err != nil {
		return c, err
	}
	if c.PasswordWebhook, err = w.hook("Private Discord password webhook (optional)", c.PasswordWebhook); err != nil {
		return c, err
	}
	return c, c.Validate()
}
