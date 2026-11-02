package reviewer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/harness"
	"cobranch.dev/internal/services/jobs"
)

// settingsPath is main's /cobranch-review.yaml as the reviewer names it.
const settingsPath = git.DefaultBranch + "'s /" + git.ReviewSettingsFile

// settings reads the repository's settings from main's /cobranch-review.yaml, the defaults without one, and ends a
// task whose branch they no longer have the reviewer review: main took a file since the task was queued.
func (rv *review) settings(ctx context.Context) (git.ReviewSettings, *harness.Kit, error) {
	f, err := rv.api.File(ctx, rv.org, rv.task.RepoSlug, git.DefaultBranch, git.ReviewSettingsFile)
	if errors.Is(err, ErrNotFound) {
		return git.DefaultReviewSettings(), nil, nil
	}
	if err != nil {
		return git.ReviewSettings{}, nil, fmt.Errorf("reading %s: %w", settingsPath, err)
	}
	s, err := code.ParseReviewSettings([]byte(f.Text))
	if f.Binary || f.Truncated {
		err = errors.New("it isn't a text file of at most 1 MiB")
	}
	if err != nil {
		end := ending(agents.TaskFailed, settingsPath+" doesn't parse: "+err.Error())
		return git.ReviewSettings{}, &end, nil
	}
	if !s.Enabled || !slices.ContainsFunc(s.Branches, func(p string) bool { return jobs.Match(p, rv.task.Branch) }) {
		end := ending(agents.TaskFailed, settingsPath+" no longer has the AI reviewer review branch "+rv.task.Branch+".")
		return git.ReviewSettings{}, &end, nil
	}
	return s, nil, nil
}

// read reads the branch's change, leaving out the files the settings ignore. It ends a task whose branch is gone or
// moved on, and reviews a change it may not read as neutral, saying why: one that changes nothing, or nothing the
// settings don't ignore, and one larger than their maxDiffLines.
func (rv *review) read(ctx context.Context, s git.ReviewSettings) (*harness.Kit, error) {
	d, err := rv.api.BranchDiff(ctx, rv.org, rv.task.RepoSlug, rv.task.Branch)
	if errors.Is(err, ErrNotFound) {
		end := ending(agents.TaskFailed, "Branch "+rv.task.Branch+" is gone.")
		return &end, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the branch's diff: %w", err)
	}
	if d.Head != rv.task.SHA {
		end := ending(agents.TaskFailed, "Branch "+rv.task.Branch+" moved on from "+short(rv.task.SHA)+" to "+short(d.Head)+
			", which a review task of its own reviews.")
		return &end, nil
	}
	rv.diff, rv.ignored = kept(d, s.Ignore)
	lines := rv.diff.Additions + rv.diff.Deletions
	switch {
	case len(d.Files) == 0 && d.FilesLeftOut == 0:
		return rv.neutral(ctx, "This change changes nothing against "+git.DefaultBranch+", so there is nothing to review.")
	case len(rv.diff.Files) == 0 && rv.diff.FilesLeftOut == 0:
		return rv.neutral(ctx, "Every file this change touches is one that "+settingsPath+" leaves out (`paths.ignore`), so I reviewed none of it.")
	case lines > s.MaxDiffLines:
		return rv.neutral(ctx, "This change has "+strconv.Itoa(lines)+" changed lines, more than the "+strconv.Itoa(s.MaxDiffLines)+
			" that "+settingsPath+" lets me review (`maxDiffLines`), so I reviewed none of it.")
	}
	return nil, nil
}

// neutral submits a neutral review that says why, and ends the task with it.
func (rv *review) neutral(ctx context.Context, why string) (*harness.Kit, error) {
	if _, err := rv.api.SubmitReview(ctx, rv.org, rv.task.RepoSlug, rv.task.SHA, code.ReviewInput{State: code.ReviewNeutral, Body: why}); err != nil {
		return nil, fmt.Errorf("submitting a neutral review: %w", err)
	}
	end := ending(agents.TaskSucceeded, why)
	return &end, nil
}

// kept is d without the files whose paths match one of the patterns, and how many it left out: its counts are those of
// the files it keeps, and of the files d itself left out, which no pattern was matched against.
func kept(d code.Diff, ignore []string) (code.Diff, int) {
	out := d
	out.Files, out.Additions, out.Deletions = nil, d.Additions, d.Deletions
	for _, f := range d.Files {
		if !slices.ContainsFunc(ignore, func(p string) bool { return jobs.Match(p, f.Path) }) {
			out.Files = append(out.Files, f)
			continue
		}
		out.Additions, out.Deletions = out.Additions-f.Additions, out.Deletions-f.Deletions
	}
	return out, len(d.Files) - len(out.Files)
}
