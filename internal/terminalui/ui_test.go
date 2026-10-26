package terminalui

import (
	"bytes"
	"strings"
	"testing"
)

func TestTableSanitizesUntrustedCellsAndStacksOnNarrowTerminal(t *testing.T) {
	var out bytes.Buffer
	u := UI{Out: &out, Width: 30}
	u.Table([]string{"Server", "Name"}, [][]string{{"server-12345678", "fake\x1b[2J\r\nnext"}})
	got := out.String()
	if strings.Contains(got, "\x1b") || strings.Contains(got, "\r") {
		t.Fatal("terminal controls escaped", got)
	}
	if !strings.Contains(got, "  Server\n") || !strings.Contains(got, "  Name\n") {
		t.Fatal("wide table not stacked", got)
	}
}
