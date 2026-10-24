package store

import (
	"errors"
	"testing"
	"time"
)

func day(s string) time.Time {
	parsed, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return parsed
}

func ptr[T any](v T) *T { return &v }

// The arithmetic a household actually asks for.
//
// A subset of RRULE on purpose: the full standard has a dozen axes nobody in a
// kitchen will ever use, and every one of them is a way for two clients to
// disagree about what a rule means.
func TestNextDueOnTheCalendar(t *testing.T) {
	cases := []struct {
		name     string
		rule     string
		catchUp  string
		previous string
		today    string
		want     string
	}{
		{
			name:     "every day",
			rule:     "FREQ=DAILY",
			previous: "2026-10-25", today: "2026-10-25", want: "2026-10-26",
		},
		{
			name:     "every third day",
			rule:     "FREQ=DAILY;INTERVAL=3",
			previous: "2026-10-25", today: "2026-10-25", want: "2026-10-28",
		},
		{
			name:     "every week, same weekday",
			rule:     "FREQ=WEEKLY",
			previous: "2026-10-25", today: "2026-10-25", want: "2026-11-01",
		},
		{
			name:     "bin day, Tuesdays",
			rule:     "FREQ=WEEKLY;BYDAY=TU",
			previous: "2026-10-20", today: "2026-10-20", want: "2026-10-27",
		},
		{
			name:     "twice a week",
			rule:     "FREQ=WEEKLY;BYDAY=TU,FR",
			previous: "2026-10-20", today: "2026-10-20", want: "2026-10-23",
		},
		{
			name:     "twice a week, wrapping to the next week",
			rule:     "FREQ=WEEKLY;BYDAY=TU,FR",
			previous: "2026-10-23", today: "2026-10-23", want: "2026-10-27",
		},
		{
			name:     "every other Tuesday keeps its parity",
			rule:     "FREQ=WEEKLY;INTERVAL=2;BYDAY=TU",
			previous: "2026-10-20", today: "2026-10-20", want: "2026-11-03",
		},
		{
			name:     "the first of the month",
			rule:     "FREQ=MONTHLY;BYMONTHDAY=1",
			previous: "2026-10-01", today: "2026-10-01", want: "2026-11-01",
		},
		{
			// A human who says "the 31st" means the end of the month, and
			// February would otherwise swallow the occurrence entirely.
			name:     "the 31st, in a month that has thirty days",
			rule:     "FREQ=MONTHLY;BYMONTHDAY=31",
			previous: "2026-10-31", today: "2026-10-31", want: "2026-11-30",
		},
		{
			name:     "the 31st, in February",
			rule:     "FREQ=MONTHLY;BYMONTHDAY=31",
			previous: "2027-01-31", today: "2027-01-31", want: "2027-02-28",
		},
		{
			// Away for two months. A weekly chore is not a debt: it comes back
			// next week, not eight times.
			name:     "catching up skips the backlog",
			rule:     "FREQ=WEEKLY;BYDAY=TU",
			catchUp:  "skip",
			previous: "2026-08-25", today: "2026-10-25", want: "2026-10-27",
		},
		{
			// And when the backlog IS the point — a medication count, a meter
			// reading — it is kept, one step at a time.
			name:     "accumulating keeps every one",
			rule:     "FREQ=WEEKLY;BYDAY=TU",
			catchUp:  "accumulate",
			previous: "2026-08-25", today: "2026-10-25", want: "2026-09-01",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			catchUp := c.catchUp
			if catchUp == "" {
				catchUp = "skip"
			}

			got, err := nextDue(
				Schedule{Mode: "calendar", Rule: c.rule, CatchUp: catchUp},
				day(c.previous), nil, day(c.today),
			)
			if err != nil {
				t.Fatalf("nextDue: %v", err)
			}
			if want := day(c.want); !got.Equal(want) {
				t.Errorf("got %s, want %s", got.Format("2006-01-02 Mon"),
					want.Format("2006-01-02 Mon"))
			}
		})
	}
}

// A relative series counts from when the thing was actually DONE, not from
// when the machine predicted it: "the sheets, three weeks after the last time"
// means three weeks after the tick.
func TestNextDueCountsFromTheTick(t *testing.T) {
	done := day("2026-10-23")

	got, err := nextDue(
		Schedule{Mode: "relative", RelDays: ptr(21), CatchUp: "skip"},
		day("2026-10-18"), &done, day("2026-10-25"),
	)
	if err != nil {
		t.Fatalf("nextDue: %v", err)
	}
	if want := day("2026-11-13"); !got.Equal(want) {
		t.Errorf("got %s, want %s: counted from the prediction rather than the tick",
			got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

func TestNextDueWithoutATickFallsBackToThePrediction(t *testing.T) {
	got, err := nextDue(
		Schedule{Mode: "relative", RelDays: ptr(7), CatchUp: "skip"},
		day("2026-10-18"), nil, day("2026-10-25"),
	)
	if err != nil {
		t.Fatalf("nextDue: %v", err)
	}
	if want := day("2026-10-25"); !got.Equal(want) {
		t.Errorf("got %s, want %s", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

// Refused loudly rather than approximated. A rule nobody can honour exactly is
// a series that quietly does the wrong thing every week for a year.
func TestARuleOutsideTheSubsetIsRefused(t *testing.T) {
	cases := map[string]struct {
		rule string
		want error
	}{
		"yearly":            {"FREQ=YEARLY", ErrRuleUnsupported},
		"a position filter": {"FREQ=MONTHLY;BYSETPOS=-1", ErrRuleUnsupported},
		"weekdays on a monthly rule": {
			"FREQ=MONTHLY;BYDAY=MO", ErrRuleUnsupported,
		},
		"no frequency":     {"INTERVAL=2", ErrRuleMalformed},
		"a zero interval":  {"FREQ=DAILY;INTERVAL=0", ErrRuleMalformed},
		"nonsense weekday": {"FREQ=WEEKLY;BYDAY=XX", ErrRuleMalformed},
		"empty":            {"", ErrRuleMalformed},
		"not a pair":       {"FREQ", ErrRuleMalformed},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := nextDue(
				Schedule{Mode: "calendar", Rule: c.rule, CatchUp: "skip"},
				day("2026-10-25"), nil, day("2026-10-25"),
			)
			if !errors.Is(err, c.want) {
				t.Errorf("got %v, want %v", err, c.want)
			}
		})
	}
}

func TestAnUnknownModeIsRefused(t *testing.T) {
	_, err := nextDue(
		Schedule{Mode: "whenever", CatchUp: "skip"},
		day("2026-10-25"), nil, day("2026-10-25"),
	)
	if !errors.Is(err, ErrRuleUnsupported) {
		t.Errorf("got %v, want ErrRuleUnsupported", err)
	}
}

// Dates are civil days carried in UTC. Doing this arithmetic in a zone with
// daylight saving is how "every day" becomes "every day except one in March".
func TestTheArithmeticDoesNotLoseADayToDaylightSaving(t *testing.T) {
	// The European clocks go forward on 2027-03-28.
	got, err := nextDue(
		Schedule{Mode: "calendar", Rule: "FREQ=DAILY", CatchUp: "accumulate"},
		day("2027-03-27"), nil, day("2027-03-27"),
	)
	if err != nil {
		t.Fatalf("nextDue: %v", err)
	}
	if want := day("2027-03-28"); !got.Equal(want) {
		t.Errorf("got %s, want %s", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}
