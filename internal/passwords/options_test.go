package passwords

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCalendarMonthClampsAndUsesUTC(t *testing.T) {
	for _, tc := range []struct {
		from, want string
		months     int
	}{{"2028-01-31T12:34:56Z", "2028-02-29T12:34:56Z", 1}, {"2027-01-31T12:34:56Z", "2027-02-28T12:34:56Z", 1}, {"2027-12-31T12:34:56Z", "2028-02-29T12:34:56Z", 2}, {"2027-02-28T12:34:56Z", "2027-03-28T12:34:56Z", 1}, {"2027-01-31T23:00:00-05:00", "2027-03-01T04:00:00Z", 1}} {
		from, _ := time.Parse(time.RFC3339, tc.from)
		got, e := NextCalendarMonth(from, tc.months)
		if e != nil || got.Format(time.RFC3339) != tc.want {
			t.Fatalf("%s +%d: %s %v", tc.from, tc.months, got, e)
		}
	}
}
func TestOptionsPreserveAssignmentsAndRotateIntoWords(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2028, 1, 31, 12, 0, 0, 0, time.UTC)
	oldStore, e := Open(dir, 24*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	old, e := oldStore.Get("game", "a", "css", now)
	if e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	s, e := OpenWithOptions(dir, Options{Format: "eff", CalendarMonths: 1})
	if e != nil {
		t.Fatal(e)
	}
	same, e := s.Get("game", "b", "css", now)
	if e != nil || same != old {
		t.Fatal("options changed retained assignment", e)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "state.json"))
	if string(before) != string(after) {
		t.Fatal("opening options rewrote assignments")
	}
	next, e := s.Get("game", "a", "css", now.Add(25*time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	if next.Password == old.Password || next.Revision == old.Revision || len(strings.Split(next.Password, "-")) != 3 {
		t.Fatal("password did not rotate to EFF words")
	}
	if next.NextAt.Format(time.RFC3339) != "2028-03-01T13:00:00Z" {
		t.Fatal(next.NextAt)
	}
	s, e = OpenWithOptions(dir, Options{Format: "eff", CalendarMonths: 1})
	if e != nil {
		t.Fatal(e)
	}
	again, _ := s.Get("game", "a", "css", now.Add(26*time.Hour))
	if again != next {
		t.Fatal("phrase changed across restart")
	}
}
func TestEmbeddedEFFWordsAndCustomCount(t *testing.T) {
	sum := sha256.Sum256([]byte(effData))
	if hex.EncodeToString(sum[:]) != "addd35536511597a02fa0a9ff1e5284677b8883b83e986e43f15a3db996b903e" {
		t.Fatal("EFF data changed")
	}
	words := map[string]bool{}
	for _, line := range strings.Split(effData, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && !strings.Contains(fields[1], "-") {
			words[fields[1]] = true
		}
	}
	s, e := OpenWithOptions(t.TempDir(), Options{Format: "eff", Words: 6})
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Get("node", "a", "css", time.Now())
	if e != nil {
		t.Fatal(e)
	}
	parts := strings.Split(a.Password, "-")
	if len(parts) != 6 {
		t.Fatal("word count", len(parts))
	}
	for _, w := range parts {
		if !words[w] {
			t.Fatal("not EFF word")
		}
	}
}
func TestInvalidOptions(t *testing.T) {
	for _, o := range []Options{{Format: "unknown"}, {Interval: -1}, {CalendarMonths: -1}, {CalendarMonths: 1, Interval: time.Hour}, {Format: "eff", Words: 1}, {Format: "eff", Words: 17}} {
		if _, e := OpenWithOptions(t.TempDir(), o); e == nil {
			t.Fatalf("accepted %+v", o)
		}
	}
	if _, e := NextCalendarMonth(time.Now(), 0); e == nil {
		t.Fatal("accepted zero months")
	}
}
