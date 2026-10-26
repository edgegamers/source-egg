package passwords

import (
	"testing"
	"time"
)

func cronTime(t *testing.T, value string) time.Time {
	t.Helper()
	result, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestNextCronBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, expression, zone, from, want string }{
		{"strict minute", "* * * * *", "", "2026-10-26T12:00:00Z", "2026-10-26T12:01:00Z"},
		{"partial minute", "* * * * *", "", "2026-10-26T12:00:59.5Z", "2026-10-26T12:01:00Z"},
		{"ranges lists steps", "5,20-50/15 8-10 * * MON-FRI", "UTC", "2026-10-26T08:20:00Z", "2026-10-26T08:35:00Z"},
		{"step from value", "10/20 * * * *", "UTC", "2026-10-26T08:30:00Z", "2026-10-26T08:50:00Z"},
		{"month name", "0 0 1 JAN,MAR *", "UTC", "2026-01-01T00:00:00Z", "2026-03-01T00:00:00Z"},
		{"Sunday zero", "0 0 * * 0", "UTC", "2026-10-26T00:00:00Z", "2026-11-01T00:00:00Z"},
		{"Sunday seven", "0 0 * * 7", "UTC", "2026-10-26T00:00:00Z", "2026-11-01T00:00:00Z"},
		{"Sunday range", "0 0 * * 5-7", "UTC", "2026-10-31T00:00:00Z", "2026-11-01T00:00:00Z"},
		{"weekday OR monthday", "0 0 31 FEB MON", "UTC", "2026-02-01T00:00:00Z", "2026-02-02T00:00:00Z"},
		{"day wildcard step intersects weekday", "0 0 */2 * MON", "UTC", "2026-10-01T00:00:00Z", "2026-10-05T00:00:00Z"},
		{"weekday wildcard step intersects monthday", "0 0 15 * */2", "UTC", "2026-10-01T00:00:00Z", "2026-10-15T00:00:00Z"},
		{"month end", "0 0 31 * *", "UTC", "2026-04-01T00:00:00Z", "2026-05-31T00:00:00Z"},
		{"leap century", "0 0 29 FEB *", "UTC", "2096-02-29T00:00:00Z", "2104-02-29T00:00:00Z"},
		{"leap century in timezone", "0 0 29 FEB *", "America/New_York", "2096-02-29T05:00:00Z", "2104-02-29T05:00:00Z"},
		{"future leap year final day", "0 0 * * *", "America/New_York", "2096-12-31T00:00:00Z", "2096-12-31T05:00:00Z"},
		{"future southern year boundary", "0 0 1 JAN *", "Australia/Sydney", "2096-12-30T13:00:00Z", "2096-12-31T13:00:00Z"},
		{"spring gap", "30 2 * * *", "America/New_York", "2026-03-07T07:30:00Z", "2026-03-09T06:30:00Z"},
		{"fall first occurrence", "30 1 * * *", "America/New_York", "2026-11-01T04:00:00Z", "2026-11-01T05:30:00Z"},
		{"fall repeated occurrence", "30 1 * * *", "America/New_York", "2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z"},
		{"fall repeated earlier wall minute", "15 1 * * *", "America/New_York", "2026-11-01T05:45:00Z", "2026-11-01T06:15:00Z"},
		{"half hour DST gap", "15 2 * * *", "Australia/Lord_Howe", "2026-10-02T15:45:00Z", "2026-10-04T15:15:00Z"},
		{"quarter hour offset", "0 0 * * *", "Asia/Kathmandu", "2026-10-26T00:00:00Z", "2026-10-26T18:15:00Z"},
		{"skipped local day", "0 0 * * *", "Pacific/Apia", "2011-12-29T10:00:00Z", "2011-12-30T10:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NextCron(cronTime(t, tc.from), tc.expression, tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(cronTime(t, tc.want)) || got.Location() != time.UTC {
				t.Fatalf("got %s, want %s UTC", got, tc.want)
			}
		})
	}
}

func TestCronRejectsInvalidAndImpossibleSchedules(t *testing.T) {
	for _, expression := range []string{"", "* * * *", "0 * * * * *", "@weekly", "CRON_TZ=UTC 0 0 * * *", "60 * * * *", "* 24 * * *", "0 0 0 * *", "0 0 * 13 *", "0 0 * * 8", "*/0 * * * *", "1,,2 * * * *", "* * * * ,", "5-2 * * * *", "0 0 31 APR *", "0 0 30 FEB *", "0 0 31 FEB */1", "0 0 31 FEB */2", "0 0 ? * *", "0 0 * * MON-FUNDAY"} {
		t.Run(expression, func(t *testing.T) {
			if _, err := NextCron(cronTime(t, "2026-10-26T00:00:00Z"), expression, "UTC"); err == nil {
				t.Fatal("accepted invalid cron")
			}
		})
	}
	for _, zone := range []string{"Local", "Not/AZone", "../UTC", "UTC+3"} {
		if _, err := NextCron(time.Now(), "0 0 * * *", zone); err == nil {
			t.Fatalf("accepted timezone %q", zone)
		}
	}
}

func TestOptionsNextScheduleAndConflicts(t *testing.T) {
	from := cronTime(t, "2026-10-26T12:00:00Z")
	for _, tc := range []struct {
		options Options
		want    string
	}{
		{Options{}, "0001-01-01T00:00:00Z"},
		{Options{Interval: time.Hour}, "2026-10-26T13:00:00Z"},
		{Options{CalendarMonths: 1}, "2026-11-26T12:00:00Z"},
		{Options{Cron: "0 0 * * *", Timezone: "America/Chicago"}, "2026-10-27T05:00:00Z"},
	} {
		got, err := tc.options.Next(from)
		if err != nil || !got.Equal(cronTime(t, tc.want)) {
			t.Fatalf("%+v got %s, %v", tc.options, got, err)
		}
	}
	for _, options := range []Options{{Cron: "0 0 * * *", Interval: time.Hour}, {Cron: "0 0 * * *", CalendarMonths: 1}, {Cron: "bad"}, {Timezone: "Not/AZone"}} {
		if err := options.Validate(); err == nil {
			t.Fatalf("accepted %+v", options)
		}
	}
}

func TestCronScheduleChangesPreserveDueDatesAndCatchUpOnce(t *testing.T) {
	dir := t.TempDir()
	originalTime := cronTime(t, "2026-10-26T12:00:00Z")
	oldStore, err := Open(dir, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	old, err := oldStore.Get("game", "a", "css", originalTime)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenWithOptions(dir, Options{Cron: "0 0 * * *", Timezone: "America/Chicago"})
	if err != nil {
		t.Fatal(err)
	}
	retained, err := store.Get("game", "a", "css", cronTime(t, "2026-10-27T06:00:00Z"))
	if err != nil || retained != old {
		t.Fatalf("changed persisted due date: %+v, %v", retained, err)
	}
	resumed := cronTime(t, "2026-11-02T12:00:00Z")
	rotated, err := store.Get("game", "a", "css", resumed)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Revision == old.Revision || !rotated.GeneratedAt.Equal(resumed) || !rotated.NextAt.Equal(cronTime(t, "2026-11-03T06:00:00Z")) {
		t.Fatalf("wrong catch-up: %+v", rotated)
	}
	again, err := store.Get("game", "a", "css", resumed.Add(time.Minute))
	if err != nil || again != rotated {
		t.Fatalf("repeated catch-up rotation: %v", err)
	}
	reopened, err := OpenWithOptions(dir, Options{Cron: "0 0 1 * *"})
	if err != nil {
		t.Fatal(err)
	}
	again, err = reopened.Get("game", "a", "css", resumed.Add(2*time.Minute))
	if err != nil || again != rotated {
		t.Fatalf("restart changed assignment: %v", err)
	}
}

func TestCronMigrationPreservesLegacyAssignmentWithNoDueDate(t *testing.T) {
	dir := t.TempDir()
	originalTime := cronTime(t, "2026-10-26T12:00:00Z")
	oldStore, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	old, err := oldStore.Get("game", "a", "css", originalTime)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenWithOptions(dir, Options{Cron: "* * * * *"})
	if err != nil {
		t.Fatal(err)
	}
	retained, err := store.Get("game", "a", "css", originalTime.AddDate(1, 0, 0))
	if err != nil || retained != old || !retained.NextAt.IsZero() {
		t.Fatalf("migration replaced never-rotating assignment: %v", err)
	}
}
