package store

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Errors a caller maps onto a refusal. A rule that does not parse is a client
// bug, never a reason to stop a series from advancing.
var (
	ErrRuleUnsupported = errors.New("store: unsupported recurrence rule")
	ErrRuleMalformed   = errors.New("store: malformed recurrence rule")
)

// Schedule is everything a series needs in order to say when its next
// occurrence falls.
//
// A DATE, never an instant. "Bin day is Tuesday" must not move because
// somebody travelled, so the arithmetic here works in civil days and the
// household's zone is applied once, at the edge, when a reminder is built.
type Schedule struct {
	// Mode is "calendar" — the schedule owns the date — or "relative", where
	// the date comes from when the thing was last actually done.
	Mode string

	// Rule is the RRULE subset, for calendar mode.
	Rule string

	// RelDays is "X days after the last time", for relative mode.
	RelDays *int

	// AnchorOn is where a calendar series starts counting.
	AnchorOn time.Time

	// CatchUp decides what a series does after a gap: "skip" lands on the next
	// date that has not passed, "accumulate" steps exactly one interval and
	// lets the occurrences pile up.
	CatchUp string
}

// nextDue computes the date of the occurrence that follows `previous`.
//
// `doneOn` is when the previous occurrence was actually ticked, which is what
// a relative series counts from: "the sheets, three weeks after the last time
// we changed them" means three weeks after the tick, not after the date the
// machine had predicted.
//
// `today` is the server's date, and the only place a clock enters. It decides
// nothing about ordering — it decides whether a series that has been asleep
// for two months starts again next week or coughs up eight overdue
// occurrences, which is a product question (`catch_up`) rather than a clock
// question.
func nextDue(s Schedule, previous time.Time, doneOn *time.Time, today time.Time) (time.Time, error) {
	previous = dayOf(previous)
	today = dayOf(today)

	switch s.Mode {
	case "relative":
		from := previous
		if doneOn != nil {
			from = dayOf(*doneOn)
		}
		if s.RelDays == nil || *s.RelDays < 1 {
			return time.Time{}, fmt.Errorf("%w: rel_days must be at least 1", ErrRuleMalformed)
		}
		return from.AddDate(0, 0, *s.RelDays), nil

	case "calendar":
		rule, err := parseRule(s.Rule)
		if err != nil {
			return time.Time{}, err
		}

		at := rule.after(previous)

		// A series nobody touched for a while. "skip" walks forward to the
		// first date that has not already passed — the chore is weekly, not a
		// debt — while "accumulate" leaves the backlog, which is what somebody
		// tracking a medication count actually wants.
		if s.CatchUp == "skip" {
			for at.Before(today) {
				at = rule.after(at)
			}
		}
		return at, nil

	default:
		return time.Time{}, fmt.Errorf("%w: mode %q", ErrRuleUnsupported, s.Mode)
	}
}

// recurrence is the parsed subset of RRULE.
//
// A SUBSET, deliberately. The full standard carries BYSETPOS, BYYEARDAY,
// WKST and a dozen other axes nobody in a household will ever use, and every
// one of them is a way for two clients to disagree about what a rule means.
// What is here covers "every day", "every Tuesday and Friday", "every other
// week", "the 1st of each month" — and anything else is refused loudly rather
// than approximated.
type recurrence struct {
	freq       string // DAILY | WEEKLY | MONTHLY
	interval   int
	byDay      []time.Weekday
	byMonthDay int
}

func parseRule(rule string) (recurrence, error) {
	out := recurrence{interval: 1}

	if strings.TrimSpace(rule) == "" {
		return out, fmt.Errorf("%w: empty", ErrRuleMalformed)
	}

	for _, part := range strings.Split(strings.ToUpper(rule), ";") {
		name, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			return out, fmt.Errorf("%w: %q", ErrRuleMalformed, part)
		}

		switch name {
		case "FREQ":
			switch value {
			case "DAILY", "WEEKLY", "MONTHLY":
				out.freq = value
			default:
				return out, fmt.Errorf("%w: FREQ=%s", ErrRuleUnsupported, value)
			}

		case "INTERVAL":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 {
				return out, fmt.Errorf("%w: INTERVAL=%s", ErrRuleMalformed, value)
			}
			out.interval = n

		case "BYDAY":
			for _, day := range strings.Split(value, ",") {
				weekday, ok := weekdayOf(day)
				if !ok {
					return out, fmt.Errorf("%w: BYDAY=%s", ErrRuleMalformed, day)
				}
				out.byDay = append(out.byDay, weekday)
			}

		case "BYMONTHDAY":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 31 {
				return out, fmt.Errorf("%w: BYMONTHDAY=%s", ErrRuleMalformed, value)
			}
			out.byMonthDay = n

		default:
			// Refused rather than ignored. Silently dropping a part of a rule
			// makes a series quietly do the wrong thing forever.
			return out, fmt.Errorf("%w: %s", ErrRuleUnsupported, name)
		}
	}

	if out.freq == "" {
		return out, fmt.Errorf("%w: no FREQ", ErrRuleMalformed)
	}
	if len(out.byDay) > 0 && out.freq != "WEEKLY" {
		return out, fmt.Errorf("%w: BYDAY outside FREQ=WEEKLY", ErrRuleUnsupported)
	}
	if out.byMonthDay > 0 && out.freq != "MONTHLY" {
		return out, fmt.Errorf("%w: BYMONTHDAY outside FREQ=MONTHLY", ErrRuleUnsupported)
	}
	return out, nil
}

// weekdayOf reads the two-letter codes RRULE uses.
//
// A function rather than a package map: a map is mutable, and the operating
// rules keep global mutable state out of this server.
func weekdayOf(code string) (time.Weekday, bool) {
	switch code {
	case "SU":
		return time.Sunday, true
	case "MO":
		return time.Monday, true
	case "TU":
		return time.Tuesday, true
	case "WE":
		return time.Wednesday, true
	case "TH":
		return time.Thursday, true
	case "FR":
		return time.Friday, true
	case "SA":
		return time.Saturday, true
	default:
		return time.Sunday, false
	}
}

// after returns the first date this rule produces strictly after `from`.
func (r recurrence) after(from time.Time) time.Time {
	switch r.freq {
	case "DAILY":
		return from.AddDate(0, 0, r.interval)

	case "WEEKLY":
		if len(r.byDay) == 0 {
			return from.AddDate(0, 0, 7*r.interval)
		}
		// Walk a day at a time to the next listed weekday. Within the same
		// week that is tomorrow or later this week; once the week is out, the
		// interval decides how many weeks to skip.
		for step := 1; step <= 7*r.interval+7; step++ {
			day := from.AddDate(0, 0, step)
			if !r.lists(day.Weekday()) {
				continue
			}
			if r.interval == 1 || weeksBetween(from, day)%r.interval == 0 {
				return day
			}
		}
		return from.AddDate(0, 0, 7*r.interval)

	case "MONTHLY":
		target := r.byMonthDay
		if target == 0 {
			target = from.Day()
		}
		// The month is stepped in arithmetic rather than with AddDate, which
		// normalises: adding a month to the 31st of October gives the 1st of
		// December, so the clamp below would already be working on the wrong
		// month. Counting months and then choosing the day keeps "the 31st"
		// meaning the end of whichever month comes next.
		total := int(from.Month()) - 1 + r.interval
		year := from.Year() + total/12
		month := time.Month(total%12 + 1)

		// The 31st in a 30-day month lands on the 30th rather than slipping
		// into the next one: a human who said "the 31st" means "the end of the
		// month", and February would otherwise swallow the occurrence.
		if last := daysIn(year, month); target > last {
			target = last
		}
		return time.Date(year, month, target, 0, 0, 0, 0, time.UTC)

	default:
		return from
	}
}

func (r recurrence) lists(day time.Weekday) bool {
	for _, listed := range r.byDay {
		if listed == day {
			return true
		}
	}
	return false
}

// weeksBetween counts whole weeks from the Monday of one date to the Monday of
// another, so "every other Tuesday" keeps its parity across a month boundary.
func weeksBetween(from, to time.Time) int {
	return int(mondayOf(to).Sub(mondayOf(from)).Hours() / (24 * 7))
}

func mondayOf(day time.Time) time.Time {
	offset := (int(day.Weekday()) + 6) % 7
	return dayOf(day).AddDate(0, 0, -offset)
}

func daysIn(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// dayOf strips everything below the day. Dates in this file are civil dates in
// the household's calendar, carried in UTC so that arithmetic on them never
// crosses a daylight-saving boundary and loses an hour.
func dayOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
