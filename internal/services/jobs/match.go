package jobs

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// Matches reports whether the job's triggers fire for e. A schedule's slots are the controller's to match: any
// schedule event matches a job with a schedule.
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
		return len(on.Schedule) > 0
	case EventManual:
		return on.Manual != nil
	}
	return false
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
