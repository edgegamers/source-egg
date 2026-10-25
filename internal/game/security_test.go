package game

import "testing"

func TestConsoleValuesRejectCommandSeparators(t *testing.T) {
	p, _ := Lookup("css")
	for _, l := range []Launch{{Port: 27015, Password: "hello;quit"}, {Port: 27015, Hostname: "bad\";quit"}} {
		if _, err := p.Command(l); err == nil {
			t.Fatal("console injection accepted")
		}
	}
}
