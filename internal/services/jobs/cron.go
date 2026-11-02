package jobs

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is a parsed five-field cron expression: the minutes, hours, days of the month, months and days of the week it
// fires at, in UTC, each as a set of bits.
type Cron struct {
	minutes, hours, days, months, weekdays uint64
	// anyDay and anyWeekday record a field that starts with *, stepped or not: cron fires on a day that either field
	// matches when neither does, and on a day both match otherwise.
	anyDay, anyWeekday bool
}

// cronField is one field of a cron expression: its name and the values it takes.
type cronField struct {
	name     string
	min, max int
}

var cronFields = [5]cronField{
	{"minute", 0, 59}, {"hour", 0, 23}, {"day of the month", 1, 31}, {"month", 1, 12}, {"day of the week", 0, 7},
}

// ParseCron parses a five-field cron expression: minute, hour, day of the month, month and day of the week (0 or 7
// is Sunday), each *, a value, a range a-b, any of those with a step /n, or a comma-separated list of them.
func ParseCron(expr string) (Cron, error) {
	fields := strings.Fields(expr)
	if len(fields) != len(cronFields) {
		return Cron{}, fmt.Errorf("a cron expression has five fields, not %d", len(fields))
	}
	var bits [5]uint64
	for i, f := range fields {
		b, err := parseCronField(f, cronFields[i])
		if err != nil {
			return Cron{}, err
		}
		bits[i] = b
	}
	if bits[4]&(1<<7) != 0 {
		bits[4] |= 1 // 7 is Sunday, as 0 is
	}
	return Cron{minutes: bits[0], hours: bits[1], days: bits[2], months: bits[3], weekdays: bits[4],
		anyDay: strings.HasPrefix(fields[2], "*"), anyWeekday: strings.HasPrefix(fields[4], "*")}, nil
}

// parseCronField parses one field of a cron expression into the set of values it takes.
func parseCronField(field string, f cronField) (uint64, error) {
	var bits uint64
	for part := range strings.SplitSeq(field, ",") {
		rng, stepText, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepText)
			if err != nil || n < 1 {
				return 0, fmt.Errorf("the %s field's step %q must be a positive number", f.name, stepText)
			}
			step = n
		}
		lo, hi, err := cronRange(rng, f, hasStep)
		if err != nil {
			return 0, err
		}
		for v := lo; v <= hi; v += step {
			bits |= 1 << v
		}
	}
	return bits, nil
}

// cronRange parses *, a value or a range a-b of one field of a cron expression; a value with a step runs to the
// field's last.
func cronRange(text string, f cronField, stepped bool) (int, int, error) {
	if text == "*" {
		return f.min, f.max, nil
	}
	loText, hiText, isRange := strings.Cut(text, "-")
	lo, err := cronValue(loText, f)
	if err != nil {
		return 0, 0, err
	}
	switch {
	case isRange:
		hi, err := cronValue(hiText, f)
		if err != nil {
			return 0, 0, err
		}
		if hi < lo {
			return 0, 0, fmt.Errorf("the %s field's range %q runs backwards", f.name, text)
		}
		return lo, hi, nil
	case stepped:
		return lo, f.max, nil
	}
	return lo, lo, nil
}

// cronValue parses one value of a field of a cron expression.
func cronValue(text string, f cronField) (int, error) {
	v, err := strconv.Atoi(text)
	if err != nil || v < f.min || v > f.max {
		return 0, fmt.Errorf("the %s field takes %d to %d, not %q", f.name, f.min, f.max, text)
	}
	return v, nil
}

// Next is the first minute after t, in UTC, that c fires at, or the zero time when it fires within no four years.
func (c Cron) Next(t time.Time) time.Time {
	t = t.UTC().Truncate(time.Minute).Add(time.Minute)
	for end := t.AddDate(4, 0, 0); t.Before(end); {
		switch {
		case c.months&(1<<uint(t.Month())) == 0:
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		case !c.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
		case c.hours&(1<<uint(t.Hour())) == 0:
			t = t.Truncate(time.Hour).Add(time.Hour)
		case c.minutes&(1<<uint(t.Minute())) == 0:
			t = t.Add(time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}

// dayMatches reports whether c fires on t's day: by its day of the month and its day of the week when either field
// starts with *, as a bare * takes every day, and by either one when both are restricted.
func (c Cron) dayMatches(t time.Time) bool {
	day := c.days&(1<<uint(t.Day())) != 0
	weekday := c.weekdays&(1<<uint(t.Weekday())) != 0
	if c.anyDay || c.anyWeekday {
		return day && weekday
	}
	return day || weekday
}
