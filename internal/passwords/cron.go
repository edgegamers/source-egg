package passwords

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Keep IANA schedules available in minimal node/container images.
)

type cronField struct {
	values   uint64
	wildcard bool
}

func (f cronField) contains(value int) bool { return f.values&(uint64(1)<<value) != 0 }

type cronSchedule struct {
	minute, hour, day, month, weekday cronField
	location                          *time.Location
}

// NextCron returns the first matching instant strictly after from, in UTC.
// It accepts five fields with lists, ranges and positive steps, month/weekday
// names, and Sunday as either 0 or 7. Day-of-month and weekday are ORed
// unless either field starts with a wildcard (including */n), which uses AND. The default timezone is UTC. Nonexistent DST minutes are skipped;
// both occurrences of repeated minutes are eligible.
func NextCron(from time.Time, expression, timezone string) (time.Time, error) {
	schedule, err := parseCron(expression, timezone)
	if err != nil {
		return time.Time{}, err
	}
	return schedule.next(from)
}

func cronLocation(name string) (*time.Location, error) {
	if name == "" {
		name = "UTC"
	}
	if name == "Local" {
		return nil, errors.New("password timezone must be an explicit IANA name, not Local")
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("invalid password timezone: %w", err)
	}
	return location, nil
}

func parseCron(expression, timezone string) (cronSchedule, error) {
	var schedule cronSchedule
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return schedule, errors.New("password cron requires exactly five fields: minute hour day-of-month month weekday")
	}
	location, err := cronLocation(timezone)
	if err != nil {
		return schedule, err
	}
	schedule.location = location
	monthNames := map[string]int{"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6, "JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12}
	weekdayNames := map[string]int{"SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6}
	for i, spec := range []struct {
		field     *cronField
		low, high int
		names     map[string]int
	}{
		{&schedule.minute, 0, 59, nil}, {&schedule.hour, 0, 23, nil}, {&schedule.day, 1, 31, nil}, {&schedule.month, 1, 12, monthNames}, {&schedule.weekday, 0, 7, weekdayNames},
	} {
		*spec.field, err = parseCronField(fields[i], spec.low, spec.high, spec.names)
		if err != nil {
			return schedule, fmt.Errorf("password cron field %d: %w", i+1, err)
		}
	}
	if schedule.weekday.contains(7) {
		schedule.weekday.values |= 1
		schedule.weekday.values &^= 1 << 7
	}
	// A weekday field without a leading wildcard can match an impossible day-of-month through
	// cron's OR rule. Otherwise validate all selected month/day combinations in
	// a leap year, the largest possible Gregorian calendar.
	if schedule.day.wildcard || !schedule.weekday.wildcard {
		return schedule, nil
	}
	for month := 1; month <= 12; month++ {
		if !schedule.month.contains(month) {
			continue
		}
		last := time.Date(2000, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
		for day := 1; day <= last; day++ {
			if schedule.day.contains(day) {
				return schedule, nil
			}
		}
	}
	return schedule, errors.New("password cron never matches a valid calendar date")
}

func parseCronField(expression string, low, high int, names map[string]int) (cronField, error) {
	result := cronField{wildcard: strings.HasPrefix(expression, "*")}
	number := func(value string) (int, error) {
		if n, ok := names[strings.ToUpper(value)]; ok {
			return n, nil
		}
		if value == "" {
			return 0, errors.New("empty value")
		}
		for _, char := range value {
			if char < '0' || char > '9' {
				return 0, errors.New("expected a number or supported name")
			}
		}
		return strconv.Atoi(value)
	}
	for _, item := range strings.Split(expression, ",") {
		parts := strings.Split(item, "/")
		if len(parts) > 2 {
			return result, errors.New("too many step separators")
		}
		step := 1
		if len(parts) == 2 {
			var err error
			step, err = strconv.Atoi(parts[1])
			if err != nil || step < 1 || strings.HasPrefix(parts[1], "+") {
				return result, errors.New("step must be a positive integer")
			}
		}
		start, end := low, high
		if parts[0] != "*" {
			bounds := strings.Split(parts[0], "-")
			if len(bounds) > 2 {
				return result, errors.New("too many range separators")
			}
			var err error
			start, err = number(bounds[0])
			if err != nil {
				return result, err
			}
			end = start
			if len(bounds) == 2 {
				end, err = number(bounds[1])
				if err != nil {
					return result, err
				}
			} else if len(parts) == 2 {
				end = high
			}
		}
		if start < low || end > high || start > end {
			return result, fmt.Errorf("range must be ascending within %d..%d", low, high)
		}
		for value := start; value <= end; {
			result.values |= uint64(1) << value
			if step > end-value {
				break
			} // Avoid overflow with a very large valid step.
			value += step
		}
	}
	return result, nil
}

func (s cronSchedule) matchesDay(day time.Time) bool {
	dom, dow := s.day.contains(day.Day()), s.weekday.contains(int(day.Weekday()))
	if s.day.wildcard || s.weekday.wildcard {
		return dom && dow
	}
	return dom || dow
}

func (s cronSchedule) next(from time.Time) (time.Time, error) {
	// Search constant-offset intervals chronologically. Inside an interval the
	// calendar can be searched as UTC wall time, so nonexistent times cannot be
	// invented and repeated times are naturally visited twice. Bound the search
	// to a full Gregorian cycle; this includes the eight-year leap-century gap.
	cursor := from.UTC().Add(time.Nanosecond)
	limit := cursor.AddDate(400, 0, 0)
	for cursor.Before(limit) {
		local := cursor.In(s.location)
		_, seconds := local.Zone()
		_, end := local.ZoneBounds()
		if end.IsZero() || end.After(limit) {
			end = limit
		}
		if !end.After(cursor) {
			// Go's POSIX-rule extension can return Jan 1 + 365 days as
			// its synthetic year end, even in a leap year. Its last offset
			// remains valid for Dec 31; do not skip that day's occurrences.
			if cursor.Month() != time.December || cursor.Day() != 31 {
				return time.Time{}, errors.New("invalid timezone transition")
			}
			end = time.Date(cursor.Year()+1, time.January, 1, 0, 0, 0, 0, time.UTC)
		}
		offset := time.Duration(seconds) * time.Second
		wallFrom, wallEnd := cursor.Add(offset), end.UTC().Add(offset)
		if wall, ok := s.nextWall(wallFrom, wallEnd); ok {
			return wall.Add(-offset).UTC(), nil
		}
		cursor = end.UTC()
	}
	return time.Time{}, errors.New("password cron has no occurrence within 400 years")
}

func (s cronSchedule) nextWall(from, end time.Time) (time.Time, bool) {
	day := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	for day.Before(end) {
		if !s.month.contains(int(day.Month())) {
			day = time.Date(day.Year(), day.Month()+1, 1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if s.matchesDay(day) {
			for hour := 0; hour < 24; hour++ {
				if !s.hour.contains(hour) {
					continue
				}
				hourStart := day.Add(time.Duration(hour) * time.Hour)
				if !hourStart.Add(time.Hour).After(from) {
					continue
				}
				for minute := 0; minute < 60; minute++ {
					if !s.minute.contains(minute) {
						continue
					}
					candidate := hourStart.Add(time.Duration(minute) * time.Minute)
					if !candidate.Before(from) {
						return candidate, candidate.Before(end)
					}
				}
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return time.Time{}, false
}
