package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestOperatorCommandRoutesRetainExplicitRestoreIntent(t *testing.T) {
	cases := []struct {
		args         []string
		method, path string
	}{{[]string{"backup", "now", "abc"}, "POST", "/backup?server=abc"}, {[]string{"backups"}, "GET", "/backups"}, {[]string{"cleanup", "now", "abc"}, "POST", "/cleanup?server=abc"}, {[]string{"maintenance", "now", "--apply"}, "POST", "/maintenance?apply=true"}, {[]string{"restore", "abc", "2", "--yes"}, "POST", "/restore?server=abc&index=2"}, {[]string{"pin", "metamod", "v1.2.3", "abc"}, "POST", "/framework-pin?name=metamod&version=v1.2.3&server=abc"}}
	for _, tc := range cases {
		method, path, _, handled, e := operatorRoute(tc.args)
		if e != nil || !handled || method != tc.method || path != tc.path {
			t.Fatalf("%v: %s %s %v %v", tc.args, method, path, handled, e)
		}
	}
	if _, _, _, _, e := operatorRoute([]string{"restore", "abc", "-1", "--yes"}); e == nil {
		t.Fatal("invalid backup index accepted")
	}
	if _, _, _, handled, _ := operatorRoute([]string{"update", "css"}); handled {
		t.Fatal("game update command overridden")
	}
}
func TestOperatorConfirmationRequiresYes(t *testing.T) {
	var out bytes.Buffer
	if confirm(strings.NewReader("n\n"), &out, "restore", false) == nil {
		t.Fatal("denied action continued")
	}
	if e := confirm(strings.NewReader(""), &out, "restore", true); e != nil {
		t.Fatal(e)
	}
}
