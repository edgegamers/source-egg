package terminalui

import (
	"fmt"
	"strings"
)

func (u UI) artLine(line, colors string) string {
	if !u.Color {
		return line
	}
	var out strings.Builder
	for i, r := range []rune(line) {
		code := "39"
		if colors[i] == 'o' {
			code = "38;5;214"
		}
		out.WriteString(u.Style(code, string(r)))
	}
	return out.String()
}

// Banner uses ordinary terminal glyphs, never terminal image protocols. On a
// narrow or ASCII-only terminal the same brands remain readable as text.
func (u UI) Banner() {
	fmt.Fprintln(u.Out)
	if !u.Unicode || u.Width < 40 {
		fmt.Fprintln(u.Out, u.Bold("  EDGEGAMERS / SOURCE ENGINE"))
		return
	}
	if u.Width < 70 {
		for i, line := range edgeLogo {
			fmt.Fprintln(u.Out, "  "+u.artLine(line, edgeLogoColors[i]))
		}
		fmt.Fprintln(u.Out, u.Bold("  EDGEGAMERS"))
		for i, line := range sourceLogo {
			fmt.Fprintln(u.Out, "  "+u.artLine(line, sourceLogoColors[i]))
		}
		fmt.Fprintln(u.Out, u.Style("33", "  SOURCE ENGINE"))
		return
	}
	for i, line := range edgeLogo {
		fmt.Fprintln(u.Out, "  "+u.artLine(line, edgeLogoColors[i])+"    "+u.artLine(sourceLogo[i], sourceLogoColors[i]))
	}
	fmt.Fprintln(u.Out, "  "+u.Bold(fmt.Sprintf("%-40s", "EDGEGAMERS"))+u.Style("33", "SOURCE ENGINE"))
}
