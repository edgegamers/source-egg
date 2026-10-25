package passwords

import (
	"testing"
	"time"
)

func TestScopeSharingAndPersistence(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	s, e := Open(dir, 24*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Get("game", "a", "css", now)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := s.Get("game", "b", "css", now)
	c, _ := s.Get("game", "c", "tf2", now)
	if a.Password == "" || a.Password != b.Password || a.Password == c.Password {
		t.Fatal("incorrect game sharing")
	}
	s, e = Open(dir, 24*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	again, _ := s.Get("game", "a", "css", now)
	if again != a {
		t.Fatal("password changed across restart")
	}
	x, _ := s.Get("server", "a", "css", now)
	y, _ := s.Get("server", "b", "css", now)
	if x.Password == y.Password {
		t.Fatal("server passwords shared")
	}
	n, _ := s.Get("node", "a", "css", now)
	m, _ := s.Get("node", "c", "tf2", now)
	if n.Password != m.Password {
		t.Fatal("node password not shared")
	}
	for _, scope := range []string{"", "fleet", "../../node"} {
		if _, e = s.Get(scope, "a", "css", now); e == nil {
			t.Fatal("accepted invalid scope", scope)
		}
	}
}
func TestRotationOnlyChangesSelectedScope(t *testing.T) {
	s, _ := Open(t.TempDir(), time.Hour)
	now := time.Now()
	a, _ := s.Get("game", "a", "css", now)
	other, _ := s.Get("game", "b", "tf2", now)
	b, _ := s.Get("game", "a", "css", now.Add(2*time.Hour))
	c, _ := s.Get("game", "b", "tf2", now)
	if a.Password == b.Password || b.Revision == a.Revision || c != other {
		t.Fatal("rotation not scoped")
	}
}
