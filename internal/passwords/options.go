package passwords

import (
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"errors"
	"math/big"
	"strings"
	"time"
)

// effData is the unmodified EFF long word list. See EFF-LICENSE.txt for source,
// authorship, and the dictionary's CC BY license.
//
//go:embed eff_large_wordlist.txt
var effData string

// Options keeps token/fixed-duration behavior by default. Format "eff" uses
// three words unless Words is supplied. CalendarMonths and Interval are exclusive.
type Options struct {
	Format         string
	Interval       time.Duration
	CalendarMonths int
	Words          int
}

func (o Options) Validate() error {
	if o.Format != "" && o.Format != "token" && o.Format != "eff" {
		return errors.New("password format must be token or eff")
	}
	if o.Interval < 0 {
		return errors.New("negative password interval")
	}
	if o.CalendarMonths < 0 || o.CalendarMonths > 1200 {
		return errors.New("password calendar months must be between 0 and 1200")
	}
	if o.CalendarMonths > 0 && o.Interval > 0 {
		return errors.New("password duration and calendar-month intervals are mutually exclusive")
	}
	if o.Words != 0 && (o.Words < 3 || o.Words > 16) {
		return errors.New("password phrase must contain between 3 and 16 words")
	}
	return nil
}

// NextCalendarMonth adds whole UTC calendar months, clamping the day to the last
// day of the destination month (January 31 becomes February 28 or 29).
func NextCalendarMonth(from time.Time, months int) (time.Time, error) {
	if months < 1 || months > 1200 {
		return time.Time{}, errors.New("calendar months must be between 1 and 1200")
	}
	from = from.UTC()
	first := time.Date(from.Year(), from.Month()+time.Month(months), 1, from.Hour(), from.Minute(), from.Second(), from.Nanosecond(), time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	return first.AddDate(0, 0, min(from.Day(), last)-1), nil
}

var effWords = func() []string {
	var words []string
	for _, line := range strings.Split(effData, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && !strings.Contains(fields[1], "-") {
			words = append(words, fields[1])
		}
	}
	return words
}()

func (o Options) generate() (string, error) {
	if o.Format != "eff" {
		var raw [18]byte
		if _, e := rand.Read(raw[:]); e != nil {
			return "", errors.New("secure password generation failed")
		}
		return base64.RawURLEncoding.EncodeToString(raw[:]), nil
	}
	count := o.Words
	if count == 0 {
		count = 3
	}
	parts := make([]string, count)
	for i := range parts {
		n, e := rand.Int(rand.Reader, big.NewInt(int64(len(effWords))))
		if e != nil {
			return "", errors.New("secure password generation failed")
		}
		parts[i] = effWords[n.Int64()]
	}
	return strings.Join(parts, "-"), nil
}
