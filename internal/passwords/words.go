package passwords

import (
	"bufio"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
)

const maxWordlistBytes = 4 << 20

var wordPattern = regexp.MustCompile(`^[a-z]{2,16}$`)
var dicePattern = regexp.MustCompile(`^[1-6]{5}$`)
var compoundPattern = regexp.MustCompile(`^[a-z]+(-[a-z]+)+$`)

// ParseWords accepts plain words or EFF dice-number/word lines. Validation
// follows upstream: at least 7772 distinct ASCII lowercase words of 2–16
// letters. The four EFF hyphenated compounds are excluded from dice-format
// lists so phrase separators remain unambiguous. Input is limited to 4 MiB.
func ParseWords(reader io.Reader) ([]string, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxWordlistBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxWordlistBytes {
		return nil, errors.New("password wordlist exceeds 4 MiB")
	}
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	seen := make(map[string]bool)
	var words []string
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		var word string
		switch {
		case len(fields) == 1:
			word = fields[0]
		case len(fields) == 2 && dicePattern.MatchString(fields[0]):
			word = fields[1]
			if len(word) <= 16 && compoundPattern.MatchString(word) {
				continue
			}
		default:
			return nil, errors.New("invalid password wordlist line")
		}
		if !wordPattern.MatchString(word) {
			return nil, errors.New("password wordlist requires lowercase ASCII words of 2–16 letters")
		}
		if !seen[word] {
			words = append(words, word)
			seen[word] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(words) < 7772 {
		return nil, errors.New("password wordlist needs at least 7772 distinct usable words")
	}
	return words, nil
}

var effWords = func() []string {
	words, err := ParseWords(strings.NewReader(effData))
	if err != nil {
		panic("invalid embedded EFF dictionary: " + err.Error())
	}
	return words
}()

func (o Options) loadWords() ([]string, error) {
	if o.Format != "eff" || o.Wordlist == "" {
		return effWords, nil
	}
	info, err := os.Stat(o.Wordlist)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("password wordlist must be a regular file")
	}
	file, err := os.Open(o.Wordlist)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return ParseWords(file)
}
