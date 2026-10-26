// SPDX-License-Identifier: GPL-3.0-or-later
// Terminal presentation adapted from S2Script CS2 Egg's internal/node/ui.
package terminalui

import (
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

type UI struct {
	Out            io.Writer
	Color, Unicode bool
	Width          int
}

func New(w io.Writer) UI {
	u := UI{Out: w, Width: 80, Unicode: true}
	if f, ok := w.(*os.File); ok {
		if _, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS); err == nil {
			u.Color = os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
			if size, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ); err == nil && size.Col > 0 {
				u.Width = int(size.Col)
			}
		}
	}
	if os.Getenv("TERM") == "dumb" || os.Getenv("LC_ALL") == "C" || os.Getenv("LC_ALL") == "POSIX" {
		u.Unicode = false
	}
	return u
}

// Plain makes externally supplied values safe for a single terminal line.
func Plain(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r >= 128 && r < 160 {
			return ' '
		}
		return r
	}, s)
}
func (u UI) Style(code, s string) string {
	s = Plain(s)
	if !u.Color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}
func (u UI) Bold(s string) string { return u.Style("1", s) }
func (u UI) Rule(title string) {
	line := "-"
	if u.Unicode {
		line = "─"
	}
	n := max(1, min(u.Width-utf8.RuneCountInString(title)-6, 54))
	fmt.Fprintf(u.Out, "\n%s %s\n", u.Style("1;34", line+line+" "+title), u.Style("90", strings.Repeat(line, n)))
}
func (u UI) Help(s string) {
	prefix := "  -> "
	if u.Unicode {
		prefix = "  ↳ "
	}
	u.wrap(s, prefix)
}
func (u UI) wrap(s, prefix string) {
	width := max(12, u.Width-utf8.RuneCountInString(prefix)-2)
	words := strings.Fields(Plain(s))
	line := ""
	for _, word := range words {
		if line != "" && utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) > width {
			fmt.Fprintln(u.Out, prefix+line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	if line != "" {
		fmt.Fprintln(u.Out, prefix+line)
	}
}
func (u UI) Step(s string) {
	mark := "+"
	if u.Unicode {
		mark = "✓"
	}
	fmt.Fprintf(u.Out, "  %s %s\n", u.Style("32", mark), Plain(s))
}
func (u UI) KV(key, value string) {
	if u.Width < 65 {
		fmt.Fprintf(u.Out, "  %s\n", u.Style("36", key))
		u.wrap(value, "    ")
		return
	}
	fmt.Fprintf(u.Out, "  %s %s\n", u.Style("36", fmt.Sprintf("%-26s", key)), Plain(value))
}
func (u UI) Table(headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = utf8.RuneCountInString(Plain(h))
	}
	for _, row := range rows {
		for i, v := range row {
			if i < len(widths) {
				widths[i] = max(widths[i], utf8.RuneCountInString(Plain(v)))
			}
		}
	}
	total := 2
	for _, w := range widths {
		total += w + 2
	}
	if total > u.Width {
		for n, row := range rows {
			if n > 0 {
				fmt.Fprintln(u.Out)
			}
			for i, v := range row {
				if i < len(headers) {
					u.KV(headers[i], v)
				}
			}
		}
		if len(rows) == 0 {
			u.Help("No entries.")
		}
		return
	}
	render := func(row []string, bold bool) {
		fmt.Fprint(u.Out, "  ")
		for i, v := range row {
			if i >= len(widths) {
				break
			}
			v = Plain(v)
			text := v + strings.Repeat(" ", widths[i]-utf8.RuneCountInString(v)+2)
			if bold {
				text = u.Bold(text)
			}
			fmt.Fprint(u.Out, text)
		}
		fmt.Fprintln(u.Out)
	}
	render(headers, true)
	for _, row := range rows {
		render(row, false)
	}
	if len(rows) == 0 {
		u.Help("No entries.")
	}
}
