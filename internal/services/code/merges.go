package code

import (
	"fmt"
	"strconv"

	"cobranch.dev/internal/services/git"
)

// Incoming is a live branch whose head a push brings into a branch with merge requirements, as it stands before the
// push: the commit it is at, its base, and its status with the checks behind it.
type Incoming struct {
	Head   string
	Base   string
	Status BranchStatus
	Checks []Check
}

// Violations lists what a push's move of target breaks of target's merge requirements, as the client sees each
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/branch-status.md, "By a push"): commits
// that no branch it brings holds, and each branch it brings that isn't Ready at the head it brings. Deleting target,
// brought nil, breaks them whole. live holds the branches it brings, by name, as they stand.
func Violations(target string, brought *git.Brought, live map[string]Incoming) []string {
	if brought == nil {
		return []string{target + " requires repo.bypass to delete it"}
	}
	var out []string
	if n := brought.Orphans; n > 0 {
		come := strconv.Itoa(n) + " commits come"
		if n == 1 {
			come = "1 commit comes"
		}
		out = append(out, fmt.Sprintf("%s requires its commits to come from Ready branches: %s from none", target, come))
	}
	for _, h := range brought.Heads {
		b, ok := live[h.Branch]
		if why := notReady(h, b, ok); why != "" {
			out = append(out, fmt.Sprintf("%s requires %s to be Ready: %s", target, h.Branch, why))
		}
	}
	return out
}

// notReady says why branch b, brought at head h, isn't Ready there, or "" when it is.
func notReady(h git.Head, b Incoming, ok bool) string {
	switch {
	case !ok || b.Head != h.SHA:
		return "it moves in this push"
	case b.Status == StatusReady:
		return ""
	case b.Status == StatusBlocked:
		return blockedBy(b)
	case b.Status == "" || b.Status == StatusProcessing:
		return "it is Processing"
	}
	return "it is " + string(b.Status)
}

// blockedBy says which of a Blocked branch's checks holds it back: the first it fails.
func blockedBy(b Incoming) string {
	short := b.Head[:min(len(b.Head), 7)]
	for _, c := range b.Checks {
		switch {
		case c.Met:
		case c.Kind == CheckAnnotation && c.Status == "":
			return c.Name + " isn't on " + short
		case c.Kind == CheckAnnotation:
			return c.Name + " failed on " + short
		case c.Kind == CheckLinearHistory && c.Count == 0:
			return "its merge commits aren't counted yet"
		case c.Kind == CheckLinearHistory:
			return plural(c.Count, "merge commit") + " among its commits"
		case c.Kind == CheckUpToDate:
			return "it is " + plural(c.Count, "commit") + " behind " + b.Base
		}
	}
	return "it is Blocked"
}

// plural counts n of a word, which takes an s for more than one.
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
