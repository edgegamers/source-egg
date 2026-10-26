package passwords

import (
	"errors"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/edgegamers/source-egg/internal/fsx"
	"github.com/edgegamers/source-egg/internal/game"
)

type Assignment struct {
	Password    string    `json:"password"`
	Revision    string    `json:"revision"`
	GeneratedAt time.Time `json:"generated_at"`
	NextAt      time.Time `json:"next_at"`
}
type Store struct {
	mu      sync.Mutex
	dir     string
	options Options
	words   []string
	records map[string]Assignment
}

var serverKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func Key(scope, server, title string) (string, error) {
	if !serverKey.MatchString(server) {
		return "", errors.New("invalid server identity")
	}
	if _, e := game.Lookup(title); e != nil {
		return "", e
	}
	switch scope {
	case "game":
		return "game:" + title, nil
	case "server":
		return "server:" + server, nil
	case "node":
		return "node", nil
	}
	return "", errors.New("password scope must be game, server, or node")
}
func Open(dir string, interval time.Duration) (*Store, error) {
	return OpenWithOptions(dir, Options{Interval: interval})
}

// OpenWithOptions applies format and schedule changes only to future
// generations. Existing assignments and their persisted due dates are retained.
func OpenWithOptions(dir string, options Options) (*Store, error) {
	if e := options.Validate(); e != nil {
		return nil, e
	}
	words, e := options.loadWords()
	if e != nil {
		return nil, e
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	if e := os.Chmod(dir, 0700); e != nil {
		return nil, e
	}
	s := &Store{dir: dir, options: options, words: words, records: map[string]Assignment{}}
	r, e := os.OpenRoot(dir)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	e = fsx.ReadJSON(r, "state.json", &s.records)
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	if s.records == nil {
		return nil, errors.New("invalid password state")
	}
	return s, nil
}
func (s *Store) Get(scope, server, title string, now time.Time) (Assignment, error) {
	key, e := Key(scope, server, title)
	if e != nil {
		return Assignment{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.records[key]
	if ok && (a.NextAt.IsZero() || now.Before(a.NextAt)) {
		return a, nil
	}
	password, e := s.options.generate(s.words)
	if e != nil {
		return a, e
	}
	next := Assignment{Password: password, Revision: fsx.Token(), GeneratedAt: now.UTC()}
	next.NextAt, e = s.options.Next(now)
	if e != nil {
		return a, e
	}
	// Publish to memory only after durable state replacement succeeds.
	records := make(map[string]Assignment, len(s.records)+1)
	for k, v := range s.records {
		records[k] = v
	}
	records[key] = next
	root, e := os.OpenRoot(s.dir)
	if e != nil {
		return Assignment{}, e
	}
	defer root.Close()
	if e = fsx.JSON(root, "state.json", records, 0600); e != nil {
		return Assignment{}, e
	}
	s.records = records
	return next, nil
}
