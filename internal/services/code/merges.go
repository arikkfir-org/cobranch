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
// that no branch it brings holds, merge commits with changes of their own, any merge commit where target requires
// linear history, and each branch it brings that isn't Ready at the head it brings. Deleting target, brought nil,
// breaks them whole. live holds the branches it brings, by name, as they stand.
func Violations(target string, linear bool, brought *git.Brought, live map[string]Incoming) []string {
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
	if why := unclean(brought.Unclean); why != "" {
		out = append(out, fmt.Sprintf("%s requires merge commits to merge cleanly: %s; make them in a Ready branch", target, why))
	}
	if linear && brought.Merges > 0 {
		out = append(out, fmt.Sprintf("%s requires linear history: the push adds %s", target, plural(brought.Merges, "merge commit")))
	}
	for _, h := range brought.Heads {
		b, ok := live[h.Branch]
		if why := notReady(h, b, ok); why != "" {
			out = append(out, fmt.Sprintf("%s requires %s to be Ready: %s", target, h.Branch, why))
		}
	}
	return out
}

// unclean names the merge commits of shas, which hold changes of their own: the first, and how many more; "" for none.
func unclean(shas []string) string {
	if len(shas) == 0 {
		return ""
	}
	first := shas[0][:min(len(shas[0]), 7)]
	if len(shas) == 1 {
		return first + " holds changes of its own"
	}
	return fmt.Sprintf("%s and %d more hold changes of their own", first, len(shas)-1)
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
		case c.Kind == CheckApprovals:
			return fmt.Sprintf("%s of %d on %s", plural(c.Count, "approval"), c.Required, short)
		case c.Kind == CheckChangesRequested:
			return c.Name + " requested changes on " + short
		case c.Kind == CheckAI:
			return aiBlocked(c, short)
		case c.Kind == CheckUpToDate:
			return "it is " + plural(c.Count, "commit") + " behind " + b.Base
		}
	}
	return "it is Blocked"
}

// aiBlocked says why the AI reviewer's check holds a branch at head short back.
func aiBlocked(c Check, short string) string {
	switch c.AI {
	case AIChangesRequested:
		return "the AI reviewer requested changes on " + short
	case AIUnreviewed:
		return "the AI reviewer hasn't reviewed " + short
	case AIFailed:
		return "the AI review of " + short + " failed: " + c.Reason
	case AICancelled:
		return "the AI review of " + short + " was cancelled: " + c.Reason
	case AINoModel:
		return "the AI reviewer has no model"
	case AISuspended:
		return "the AI reviewer is suspended"
	case AIOff:
		return "main's /cobranch-review.yaml leaves it out of the AI reviewer's reviews"
	case AIMissing:
		return "the organization has no AI reviewer"
	}
	return "the AI reviewer didn't approve " + short
}

// plural counts n of a word, which takes an s for more than one.
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
