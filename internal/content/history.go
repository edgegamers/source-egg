package content

import (
	"errors"
	"github.com/edgegamers/source-egg/internal/game"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (s *Store) Generation(id game.Identity, key string) (Generation, error) {
	if err := id.Validate(); err != nil {
		return Generation{}, err
	}
	if !filepath.IsLocal(key) || filepath.Clean(key) != key || strings.Count(key, "/") != 4 || !strings.HasPrefix(key, "generations/"+id.Key()+"/") {
		return Generation{}, errors.New("generation does not belong to this game/branch/runtime")
	}
	path := filepath.Join(s.Dir, key)
	build, err := InstalledBuild(path, id)
	if err != nil {
		return Generation{}, err
	}
	return Generation{Identity: id, Key: key, BuildID: build, Path: path}, nil
}
func (s *Store) History(id game.Identity) ([]Generation, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	dir := "generations/" + id.Key()
	entries, err := os.ReadDir(filepath.Join(s.Dir, dir))
	if err != nil {
		return nil, err
	}
	var out []Generation
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		g, err := s.Generation(id, dir+"/"+entry.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key > out[j].Key })
	return out, nil
}
