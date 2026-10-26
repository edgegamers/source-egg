package passwords

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func dictionaryFixture(prefix string) string {
	var words strings.Builder
	for n := 0; n < 7772; n++ {
		words.WriteString(prefix)
		words.WriteByte(byte('a' + n/(26*26)))
		words.WriteByte(byte('a' + n/26%26))
		words.WriteByte(byte('a' + n%26))
		words.WriteByte('\n')
	}
	return words.String()
}

func TestParseWordsAcceptsEFFAndPlainDictionaries(t *testing.T) {
	for _, dictionary := range []string{effData, dictionaryFixture("custom")} {
		words, err := ParseWords(strings.NewReader(dictionary))
		if err != nil {
			t.Fatal(err)
		}
		if len(words) != 7772 {
			t.Fatalf("got %d usable words", len(words))
		}
		for _, word := range words {
			if strings.Contains(word, "-") {
				t.Fatal("compound included in phrase dictionary")
			}
		}
	}
	words, err := ParseWords(strings.NewReader(dictionaryFixture("custom") + "customaaa\n\n"))
	if err != nil || len(words) != 7772 {
		t.Fatalf("duplicates changed entropy count: %d %v", len(words), err)
	}
}

func TestParseWordsRejectsUnsafeInsufficientOrOversizedInput(t *testing.T) {
	valid := dictionaryFixture("custom")
	for _, invalid := range []string{"UPPER", "abc;quit", "abc--def", "a", "abcdefghijklmnopq", "naïve", "word extra", "11117 word", "11111 -apple", "11111 apple--pear", "apple-pear"} {
		t.Run(invalid, func(t *testing.T) {
			if _, err := ParseWords(strings.NewReader(valid + invalid + "\n")); err == nil {
				t.Fatal("accepted unsafe entry")
			}
		})
	}
	_, short, _ := strings.Cut(valid, "\n")
	for _, invalid := range []string{short, strings.Repeat("apple\n", 7772), strings.Repeat("a", (4<<20)+1)} {
		if _, err := ParseWords(strings.NewReader(invalid)); err == nil {
			t.Fatal("accepted dictionary without required entropy or bound")
		}
	}
}

func TestCustomDictionaryControlsFuturePasswordsAndPreservesAssignments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "words.txt")
	if err := os.WriteFile(path, []byte(dictionaryFixture("first")), 0600); err != nil {
		t.Fatal(err)
	}
	options := Options{Format: "eff", Wordlist: path, Words: 4, Interval: time.Hour}
	store, err := OpenWithOptions(filepath.Join(dir, "state"), options)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	original, err := store.Get("game", "a", "css", now)
	if err != nil {
		t.Fatal(err)
	}
	if parts := strings.Split(original.Password, "-"); len(parts) != 4 {
		t.Fatal("wrong word count")
	} else {
		for _, word := range parts {
			if !strings.HasPrefix(word, "first") {
				t.Fatal("custom dictionary ignored")
			}
		}
	}
	if err := os.WriteFile(path, []byte(dictionaryFixture("second")), 0600); err != nil {
		t.Fatal(err)
	}
	store, err = OpenWithOptions(filepath.Join(dir, "state"), options)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := store.Get("game", "a", "css", now.Add(time.Minute))
	if err != nil || retained != original {
		t.Fatalf("dictionary changed saved assignment: %v", err)
	}
	rotated, err := store.Get("game", "a", "css", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range strings.Split(rotated.Password, "-") {
		if !strings.HasPrefix(word, "second") {
			t.Fatal("rotation ignored updated dictionary")
		}
	}
}

func TestInvalidCustomDictionaryDoesNotCreatePasswordState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "words.txt")
	if err := os.WriteFile(path, []byte("apple\npear\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, dictionary := range []string{path, filepath.Join(dir, "missing.txt"), "relative.txt"} {
		state := filepath.Join(dir, "new-state")
		if _, err := OpenWithOptions(state, Options{Format: "eff", Wordlist: dictionary}); err == nil {
			t.Fatal("accepted unusable custom dictionary")
		}
		if _, err := os.Stat(state); !os.IsNotExist(err) {
			t.Fatal("invalid dictionary created state directory", err)
		}
	}
}
