package reviewer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/harness"
)

// The tools a review task gets.
const (
	readDiffName = "read_diff"
	submitName   = "submit_review"
)

// pageBytes is about the most of the diff one call of read_diff answers, short of what the harness lets a call answer.
const pageBytes = harness.MaxOutput - 8<<10

// readDiff reads the change a review task reviews, a page at a time.
type readDiff struct {
	rv *review
}

// Spec describes read_diff to the model (harness.Tool).
func (readDiff) Spec() harness.Spec {
	return harness.Spec{
		Name: readDiffName,
		Description: "Reads the change you review, a page of files at a time, from the file numbered from (0, the first, when left out). " +
			"Each line of a hunk shows its number on " + git.DefaultBranch + "'s side of the diff (old), then on the commit's (new), then + for a " +
			"line the change adds, - for one it removes, or a space for one around them, then the line. The page ends saying where the next starts.",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"from":{"type":"integer","minimum":0,"description":"The number of the file the page starts at; 0 when left out"}}}`),
	}
}

// Run answers the page of the diff that starts at the file the call names (harness.Tool).
func (t readDiff) Run(_ context.Context, c harness.Call) (string, error) {
	var args struct {
		From int `json:"from"`
	}
	if err := json.Unmarshal(c.Arguments, &args); err != nil {
		return "", fmt.Errorf("read_diff takes an object of from, a number: %w", err)
	}
	if n := len(t.rv.diff.Files); args.From < 0 || args.From >= n {
		return "", fmt.Errorf("from is a file's number, from 0 to %d", n-1)
	}
	return t.rv.page(args.From), nil
}

// page is the diff's files from the one numbered from, as many as pageBytes holds, and at least that one: a file that
// would overflow it starts the next page.
func (rv *review) page(from int) string {
	d := rv.diff
	var b strings.Builder
	fmt.Fprintf(&b, "Branch %s at %s against %s: %d files, +%d -%d lines.", rv.task.Branch, short(d.Head), git.DefaultBranch,
		len(d.Files)+d.FilesLeftOut, d.Additions, d.Deletions)
	if rv.ignored > 0 {
		fmt.Fprintf(&b, " %s leaves %d more out (`paths.ignore`).", settingsPath, rv.ignored)
	}
	if d.FilesLeftOut > 0 {
		fmt.Fprintf(&b, " %d of them are past the most a diff lists: read them in your workspace.", d.FilesLeftOut)
	}
	to := from
	for ; to < len(d.Files); to++ {
		f := fileText(d.Files[to], pageBytes)
		if to > from && b.Len()+len(f) > pageBytes {
			break
		}
		b.WriteString("\n\n" + f)
	}
	fmt.Fprintf(&b, "\n\nFiles %d to %d of %d.", from, to-1, len(d.Files))
	if to < len(d.Files) {
		fmt.Fprintf(&b, " Call %s with from %d for the rest.", readDiffName, to)
	}
	return b.String()
}

// fileText is f's change, cut short of limit bytes.
func fileText(f code.FileDiff, limit int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s (%s, +%d -%d)", f.Path, f.Status, f.Additions, f.Deletions)
	if f.OldPath != "" && f.OldPath != f.Path {
		fmt.Fprintf(&b, ", from %s", f.OldPath)
	}
	switch {
	case f.Binary:
		b.WriteString("\n\nA binary file: the diff shows no lines of it.")
	case f.Truncated:
		b.WriteString("\n\nToo large for the diff to show: read it in your workspace.")
	}
	const cutNote = "\n\n[Cut: this file's change is longer than a page holds: read the rest in your workspace.]"
	for _, h := range f.Hunks {
		lines := []string{fmt.Sprintf("\n\n@@ -%d,%d +%d,%d @@ %s", h.OldStart, h.OldLines, h.NewStart, h.NewLines, h.Section)}
		for _, l := range h.Lines {
			lines = append(lines, fmt.Sprintf("\n%6s %6s %s %s", number(l.Old), number(l.New), marks[l.Kind], l.Text))
		}
		for _, l := range lines {
			if b.Len()+len(l)+len(cutNote) > limit {
				return b.String() + cutNote
			}
			b.WriteString(l)
		}
	}
	return b.String()
}

// marks are the marks of a diff's lines, by kind.
var marks = map[code.LineKind]string{code.LineContext: " ", code.LineAdded: "+", code.LineRemoved: "-"}

// number is a line's number on one side, blank for a line that side lacks.
func number(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// submitReview submits the review a review task writes, once.
type submitReview struct {
	rv *review
}

// Spec describes submit_review to the model (harness.Tool).
func (submitReview) Spec() harness.Spec {
	return harness.Spec{
		Name: submitName,
		Description: "Submits your review of the commit, once: its verdict, its body and its comments, each on lines of one file of the change. " +
			"A comment's lines are on the commit's side of the diff (new) unless it says old, for lines the change removes.",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"state":{"type":"string","enum":["approved","changes_requested","neutral"],"description":"approved when nothing must change before it merges; changes_requested when something must; neutral when you can't judge it"},` +
			`"body":{"type":"string","description":"The review's summary, in Markdown, at most 65,536 characters; changes_requested and neutral need it unless their comments say what"},` +
			`"comments":{"type":"array","maxItems":100,"items":{"type":"object","properties":{` +
			`"path":{"type":"string","description":"The file's path, as the diff names it"},` +
			`"side":{"type":"string","enum":["new","old"],"description":"new, the commit's side, when left out; old for lines the change removes"},` +
			`"line":{"type":"integer","minimum":1,"description":"The first line it is about, by its number on that side"},` +
			`"endLine":{"type":"integer","minimum":1,"description":"The last line it is about; line when left out"},` +
			`"body":{"type":"string","description":"The comment, in Markdown, at most 20,000 characters"}},` +
			`"required":["path","line","body"]}}},"required":["state"]}`),
	}
}

// submitArgs are submit_review's arguments.
type submitArgs struct {
	State    code.ReviewState `json:"state"`
	Body     string           `json:"body"`
	Comments []struct {
		Path    string `json:"path"`
		Side    string `json:"side"`
		Line    int    `json:"line"`
		EndLine int    `json:"endLine"`
		Body    string `json:"body"`
	} `json:"comments"`
}

// Run submits the review the call says, unless the task submitted one already (harness.Tool).
func (t submitReview) Run(ctx context.Context, c harness.Call) (string, error) {
	rv := t.rv
	if done, err := rv.submitted(ctx); err != nil || done != nil {
		if err != nil {
			return "", err
		}
		return "You submitted your review of " + short(rv.task.SHA) + " already: " + string(done.State) + ". Call finish now.", nil
	}
	in, err := rv.input(c.Arguments)
	if err != nil {
		return "", err
	}
	if _, err := rv.api.SubmitReview(ctx, rv.org, rv.task.RepoSlug, rv.task.SHA, in); err != nil {
		return "", fmt.Errorf("submitting the review: %w", err)
	}
	return fmt.Sprintf("Submitted your review of %s, %s, with %d comments. Call finish now, with your verdict as its summary.",
		short(rv.task.SHA), in.State, len(in.Comments)), nil
}

// input reads submit_review's arguments into the review they say, anchoring each comment on its side of the diff.
func (rv *review) input(args json.RawMessage) (code.ReviewInput, error) {
	var a submitArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return code.ReviewInput{}, fmt.Errorf("submit_review takes an object of state, body and comments: %w", err)
	}
	in := code.ReviewInput{State: a.State, Body: a.Body}
	for i, c := range a.Comments {
		sha := rv.task.SHA
		switch c.Side {
		case "", "new":
		case "old":
			if sha = rv.diff.Base; sha == "" {
				return code.ReviewInput{}, fmt.Errorf("comment %d is on the old side, but the change starts from nothing", i)
			}
		default:
			return code.ReviewInput{}, fmt.Errorf("comment %d's side is new or old", i)
		}
		end := max(c.EndLine, c.Line)
		in.Comments = append(in.Comments, code.ReviewComment{Anchor: code.Anchor{SHA: sha, Path: c.Path, StartLine: c.Line, EndLine: end}, Body: c.Body})
	}
	if len(in.Comments) > 0 {
		in.Branch = rv.task.Branch
	}
	if err := code.ValidateReview(in); err != nil {
		return code.ReviewInput{}, errors.Join(errors.New("the review isn't one Cobranch takes"), err)
	}
	return in, nil
}
