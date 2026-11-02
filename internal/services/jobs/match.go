package jobs

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Matches reports whether the job's triggers fire for e. A schedule's slots are the controller's to match (DueSlot):
// a schedule event matches the job it names, when the job has a schedule.
func (j Job) Matches(e Event) bool {
	on := j.On
	branch, isBranch := strings.CutPrefix(e.Ref, "refs/heads/")
	switch e.Kind {
	case EventPush:
		return on.Push != nil && isBranch && anyMatch(on.Push.Branches, branch)
	case EventTag:
		tag, isTag := strings.CutPrefix(e.Ref, "refs/tags/")
		return on.Tag != nil && isTag && anyMatch(on.Tag.Tags, tag)
	case EventAnnotation:
		return on.Annotation != nil && isBranch && e.Name != j.Name && slices.Contains(on.Annotation.Names, e.Name) &&
			slices.Contains(on.Annotation.Statuses, e.State) && anyMatch(on.Annotation.Branches, branch)
	case EventReview:
		return on.Review != nil && isBranch && slices.Contains(on.Review.States, e.State) && anyMatch(on.Review.Branches, branch)
	case EventPosted:
		return on.Event != nil && slices.Contains(on.Event.Names, e.Name)
	case EventSchedule:
		return len(on.Schedule) > 0 && e.Name == j.Name
	case EventManual:
		return on.Manual != nil && e.Name == j.Name && isBranch
	}
	return false
}

// Follows reports whether the job has a trigger for events of kind at all, whatever they say: a change that may
// trigger it asks first, so that one no job follows needn't take the repository's run queue.
func (j Job) Follows(kind EventKind) bool {
	on := j.On
	switch kind {
	case EventPush:
		return on.Push != nil
	case EventTag:
		return on.Tag != nil
	case EventAnnotation:
		return on.Annotation != nil
	case EventReview:
		return on.Review != nil
	case EventPosted:
		return on.Event != nil
	case EventSchedule:
		return len(on.Schedule) > 0
	case EventManual:
		return on.Manual != nil
	}
	return false
}

// DueSlot is the latest minute within late of now, now included, that one of the job's schedules fires at, and true;
// false when none fires then. A controller that queues each slot once, on a pass at least every late, so misses none,
// and one that was down longer skips the slots it missed.
func (j Job) DueSlot(now time.Time, late time.Duration) (time.Time, bool) {
	var due time.Time
	for _, s := range j.On.Schedule {
		c, err := ParseCron(s.Cron)
		if err != nil {
			continue // the file's parser refused it already; a definition kept from before then fires never
		}
		// Next skips the minute its time falls in: from a nanosecond earlier, a slot exactly late ago counts.
		for slot := c.Next(now.Add(-late - time.Nanosecond)); !slot.IsZero() && !slot.After(now); slot = c.Next(slot) {
			if slot.After(due) {
				due = slot
			}
		}
	}
	return due, !due.IsZero()
}

// anyMatch reports whether name matches one of patterns, or whether there are none, which matches every name.
func anyMatch(patterns []string, name string) bool {
	if len(patterns) == 0 {
		return true
	}
	return slices.ContainsFunc(patterns, func(p string) bool { return Match(p, name) })
}

// Match reports whether a branch's or a tag's name matches pattern: * matches any characters but /, ** any at all, ?
// one character but /, and every other character itself. So release/* matches release/1.0 but not release/1.0/rc,
// which release/** matches.
func Match(pattern, name string) bool {
	for len(pattern) > 0 {
		switch {
		case pattern[0] == '*':
			return matchStars(pattern, name)
		case len(name) == 0, pattern[0] == '?' && name[0] == '/', pattern[0] != '?' && pattern[0] != name[0]:
			return false
		}
		size := 1
		if pattern[0] == '?' {
			_, size = utf8.DecodeRuneInString(name)
		}
		pattern, name = pattern[1:], name[size:]
	}
	return len(name) == 0
}

// matchStars reports whether name matches pattern, which starts with * or **: whether the rest of the pattern matches
// what is left of name once the stars take some of its start, up to its first / for one star.
func matchStars(pattern, name string) bool {
	deep := strings.HasPrefix(pattern, "**")
	rest := strings.TrimLeft(pattern, "*")
	for i := 0; i <= len(name); i++ {
		if Match(rest, name[i:]) {
			return true
		}
		if !deep && i < len(name) && name[i] == '/' {
			return false
		}
	}
	return false
}
