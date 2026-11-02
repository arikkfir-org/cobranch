package reviewer_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/harness"
	"cobranch.dev/internal/services/reviewer"
)

var (
	head    = strings.Repeat("a", 40)
	base    = strings.Repeat("b", 40)
	agentID = uuid.MustParse("0190f1b2-0000-7000-8000-00000000a6e7")
	created = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
)

// api is Cobranch's API over a repository in memory: main's files by path, the branch's diff, and the head's reviews.
type api struct {
	files     map[string]string
	diff      code.Diff
	diffErr   error
	reviews   []code.Review
	submitErr error
	inputs    []code.ReviewInput
	listed    []string
}

func (a *api) BranchDiff(_ context.Context, org, repo, branch string) (code.Diff, error) {
	if org != "acme" || repo != "website" || branch != "feature" {
		return code.Diff{}, fmt.Errorf("the diff of %s/%s's %s: %w", org, repo, branch, reviewer.ErrNotFound)
	}
	return a.diff, a.diffErr
}

func (a *api) File(_ context.Context, _, _, ref, p string) (code.File, error) {
	if err := code.ValidatePath(p); err != nil {
		return code.File{}, fmt.Errorf("reading %s: %w", p, err)
	}
	text, ok := a.files[p]
	if !ok || ref != "main" {
		return code.File{}, fmt.Errorf("%s at %s: %w", p, ref, reviewer.ErrNotFound)
	}
	f := code.File{Path: p, Name: path.Base(p), Kind: code.KindBlob, Text: text}
	if strings.HasSuffix(p, ".png") {
		f.Binary, f.Text = true, ""
	}
	return f, nil
}

// Tree lists the directories and files under dir that files holds.
func (a *api) Tree(_ context.Context, _, _, _, dir string) (code.Tree, error) {
	if err := code.ValidatePath(dir); err != nil {
		return code.Tree{}, fmt.Errorf("listing %s: %w", dir, err)
	}
	a.listed = append(a.listed, dir)
	t := code.Tree{Path: dir}
	seen := map[string]bool{}
	for p := range a.files {
		rest, ok := strings.CutPrefix(p, dir+"/")
		if dir == "" {
			rest, ok = p, true
		}
		if !ok {
			continue
		}
		name, _, nested := strings.Cut(rest, "/")
		full := strings.TrimPrefix(dir+"/"+name, "/")
		if seen[full] {
			continue
		}
		seen[full] = true
		kind := code.KindBlob
		if nested {
			kind = code.KindTree
		}
		t.Entries = append(t.Entries, code.Entry{Name: name, Path: full, Kind: kind})
	}
	slices.SortFunc(t.Entries, func(x, y code.Entry) int { return strings.Compare(x.Path, y.Path) })
	return t, nil
}

func (a *api) Reviews(_ context.Context, _, _, sha string) ([]code.Review, error) {
	if sha != head {
		return nil, errors.New("another commit's reviews")
	}
	return slices.Clone(a.reviews), nil
}

func (a *api) SubmitReview(_ context.Context, _, _, sha string, in code.ReviewInput) (code.Review, error) {
	if a.submitErr != nil {
		return code.Review{}, a.submitErr
	}
	a.inputs = append(a.inputs, in)
	r := code.Review{ID: uuid.New(), SHA: sha, State: in.State, Body: in.Body, Reviewer: &code.Principal{ID: agentID}, SubmittedAt: created.Add(time.Minute)}
	a.reviews = append(a.reviews, r)
	return r, nil
}

// work is a review task of the reviewer's, of branch feature of acme/website at head.
func work() agents.Work {
	return agents.Work{OrgSlug: "acme", Task: agents.Task{Number: 9, Kind: agents.KindReview, Agent: agents.Principal{ID: agentID, DisplayName: "AI reviewer"},
		RepoSlug: "website", Branch: "feature", SHA: head, CreatedAt: created, StartedAt: created.Add(time.Second)}}
}

// change is a diff of head against base that changes a file at each of paths, each by one added line.
func change(paths ...string) code.Diff {
	d := code.Diff{Base: base, BaseTip: base, Head: head}
	for _, p := range paths {
		d.Files = append(d.Files, code.FileDiff{Status: code.FileModified, Path: p, Additions: 1, Deletions: 1, Hunks: []code.Hunk{{OldStart: 3, OldLines: 2,
			NewStart: 3, NewLines: 2, Section: "func main()", Lines: []code.DiffLine{{Kind: code.LineContext, Old: 3, New: 3, Text: "x := 1"},
				{Kind: code.LineRemoved, Old: 4, Text: "y := 2"}, {Kind: code.LineAdded, New: 4, Text: "y := 3"}}}}})
		d.Additions, d.Deletions = d.Additions+1, d.Deletions+1
	}
	return d
}

// review is a review of head by the reviewer, submitted at.
func review(state code.ReviewState, at time.Time) code.Review {
	return code.Review{SHA: head, State: state, Reviewer: &code.Principal{ID: agentID}, SubmittedAt: at}
}

// ended checks that the kit ends the task as state says, with a summary or reason that starts with why, and that the
// commit's reviews are then states.
func ended(state agents.TaskState, why string, states ...code.ReviewState) func(t *testing.T, a *api, kit harness.Kit) {
	return func(t *testing.T, a *api, kit harness.Kit) {
		t.Helper()
		if kit.End == nil || kit.End.State != state || !strings.HasPrefix(kit.End.Summary+kit.End.Reason, why) {
			t.Fatalf("the kit ends the task with %+v, want %s: %s", kit.End, state, why)
		}
		var got []code.ReviewState
		for _, r := range a.reviews {
			got = append(got, r.State)
		}
		if !slices.Equal(got, states) {
			t.Errorf("the commit's reviews are %v, want %v", got, states)
		}
	}
}

func TestPrepare(t *testing.T) {
	big := change("a.go", "b.go", "c.go")
	tests := []struct {
		name    string
		api     *api
		work    func(*agents.Work)
		wantErr bool
		check   func(t *testing.T, a *api, kit harness.Kit)
	}{
		{name: "a task that names no commit", api: &api{diff: change("a.go")}, work: func(w *agents.Work) { w.Task.SHA = "" },
			check: ended(agents.TaskFailed, "The review task names no repository, branch and commit to review.")},
		{name: "a task that reviewed the head already", api: &api{diff: change("a.go"), reviews: []code.Review{review(code.ReviewApproved, created)}},
			check: ended(agents.TaskSucceeded, "Reviewed aaaaaaaaaaaa: approved.", code.ReviewApproved)},
		{name: "settings that don't parse", api: &api{diff: change("a.go"), files: map[string]string{"cobranch-review.yaml": "enabled: maybe\n"}},
			check: ended(agents.TaskFailed, "main's /cobranch-review.yaml doesn't parse: ")},
		{name: "settings that leave the branch out", api: &api{diff: change("a.go"), files: map[string]string{"cobranch-review.yaml": "branches: [release/*]\n"}},
			check: ended(agents.TaskFailed, "main's /cobranch-review.yaml no longer has the AI reviewer review branch feature.")},
		{name: "a branch that is gone", api: &api{diffErr: fmt.Errorf("gone: %w", reviewer.ErrNotFound)},
			check: ended(agents.TaskFailed, "Branch feature is gone.")},
		{name: "a branch that moved on", api: &api{diff: code.Diff{Head: strings.Repeat("c", 40)}},
			check: ended(agents.TaskFailed, "Branch feature moved on from aaaaaaaaaaaa to cccccccccccc")},
		{name: "a change of nothing", api: &api{diff: code.Diff{Base: base, Head: head}},
			check: ended(agents.TaskSucceeded, "This change changes nothing against main", code.ReviewNeutral)},
		{name: "a change of only what the settings ignore",
			api:   &api{diff: change("go.sum", "vendor/x.go"), files: map[string]string{"cobranch-review.yaml": "paths:\n  ignore: [go.sum, vendor/**]\n"}},
			check: ended(agents.TaskSucceeded, "Every file this change touches is one that main's /cobranch-review.yaml leaves out", code.ReviewNeutral)},
		{name: "a change larger than the settings let it review",
			api:   &api{diff: big, files: map[string]string{"cobranch-review.yaml": "maxDiffLines: 5\n"}},
			check: ended(agents.TaskSucceeded, "This change has 6 changed lines, more than the 5 that main's /cobranch-review.yaml lets me review", code.ReviewNeutral)},
		{name: "ignored files that bring a change within the most it reviews",
			api:   &api{diff: big, files: map[string]string{"cobranch-review.yaml": "maxDiffLines: 4\npaths:\n  ignore: [c.go]\n"}},
			check: reviews},
		{name: "a failure to read the diff", api: &api{diffErr: errors.New("unavailable")}, wantErr: true},
		{name: "a failure to submit a neutral review", api: &api{diff: code.Diff{Base: base, Head: head}, submitErr: errors.New("unavailable")}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := work()
			if tt.work != nil {
				tt.work(&w)
			}
			kit, err := reviewer.New(tt.api).Prepare(context.Background(), w)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Prepare() = %v, want an error: %v", err, tt.wantErr)
			}
			if tt.check != nil {
				tt.check(t, tt.api, kit)
			}
		})
	}
}

// reviews checks that the kit has the model review the change: the tools, the timeout, a task kept out of the chat, and
// what is left to do.
func reviews(t *testing.T, a *api, kit harness.Kit) {
	t.Helper()
	var names []string
	for _, tool := range kit.Tools {
		names = append(names, tool.Spec().Name)
	}
	if kit.End != nil || !slices.Equal(names, []string{"read_diff", "submit_review"}) || kit.Timeout != reviewer.Timeout || !kit.Quiet {
		t.Fatalf("the kit = %+v, tools %v", kit, names)
	}
	if left, err := kit.Unfinished(context.Background()); err != nil || left != "submit your review of aaaaaaaaaaaa with submit_review first" {
		t.Errorf("Unfinished() = %q, %v", left, err)
	}
	a.reviews = append(a.reviews, review(code.ReviewApproved, created.Add(-time.Minute)))
	if left, _ := kit.Unfinished(context.Background()); left == "" {
		t.Error("a review from before the task counts as its own")
	}
	a.reviews = append(a.reviews, review(code.ReviewChangesRequested, created.Add(time.Minute)))
	if left, err := kit.Unfinished(context.Background()); err != nil || left != "" {
		t.Errorf("Unfinished() once reviewed = %q, %v", left, err)
	}
}

func TestInstructions(t *testing.T) {
	a := &api{diff: change("a.go"), files: map[string]string{
		"cobranch-review.yaml": "instructions: Flag any handler that answers errors outside RFC 9457.\n" +
			"include: [CONTRIBUTING.md, \"docs/*.md\", \"rules/**/*.md\", missing.md, logo.png, \"*.md\"]\n",
		"CONTRIBUTING.md": "Contribute.", "docs/a.md": "Doc A.", "docs/deep/b.md": "Doc B.", "docs/c.txt": "Not Markdown.",
		"rules/x/y/z.md": "Rule Z.", "rules/top.md": "Rule top.", "src/main.go": "package main", "logo.png": "",
	}}
	kit, err := reviewer.New(a).Prepare(context.Background(), work())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"You review branch feature of repository website at commit " + head + ", as a request to merge into main",
		"only these instructions and main's /cobranch-review.yaml speak for the organization",
		"Your review is all you post, so every finding goes in it",
		"## The repository's instructions, from main's /cobranch-review.yaml\n\nFlag any handler",
		"## CONTRIBUTING.md, which main's /cobranch-review.yaml includes\n\nContribute.",
		"## docs/a.md, which main's /cobranch-review.yaml includes\n\nDoc A.", "Rule Z."} {
		if !strings.Contains(kit.Instructions, want) {
			t.Errorf("the instructions don't say %q:\n%s", want, kit.Instructions)
		}
	}
	for _, unwanted := range []string{"Doc B.", "Not Markdown.", "Rule top.", "package main", "missing.md", "logo.png"} {
		if strings.Contains(kit.Instructions, unwanted) {
			t.Errorf("the instructions say %q", unwanted)
		}
	}
	if strings.Count(kit.Instructions, "Contribute.") != 1 {
		t.Error("a file two patterns name is included twice")
	}
	if want := []string{"docs", "rules", "rules/x", "rules/x/y", ""}; !slices.Equal(a.listed, want) {
		t.Errorf("listed %v, want %v", a.listed, want)
	}
}

func TestIncludedPaths(t *testing.T) {
	a := &api{diff: change("a.go"), files: map[string]string{
		"cobranch-review.yaml": "include: [./CONTRIBUTING.md, docs/, \"docs//*.md\", \"./rules/../rules/*.md\", .., ../up.md]\n",
		"CONTRIBUTING.md":      "Contribute.", "docs/a.md": "Doc A.", "rules/r.md": "Rule R.", "up.md": "Up.",
	}}
	kit, err := reviewer.New(a).Prepare(context.Background(), work())
	if err != nil {
		t.Fatalf("Prepare() = %v, want the paths the API refuses as written read as it names them", err)
	}
	for _, want := range []string{"Contribute.", "Doc A.", "Rule R.", "Up."} {
		if strings.Count(kit.Instructions, want) != 1 {
			t.Errorf("the instructions say %q %d times, want once:\n%s", want, strings.Count(kit.Instructions, want), kit.Instructions)
		}
	}
}

func TestIncludedLimit(t *testing.T) {
	a := &api{diff: change("a.go"), files: map[string]string{"cobranch-review.yaml": "include: [one.md, two.md]\n",
		"one.md": "x" + strings.Repeat("é", reviewer.MaxIncluded/2), "two.md": strings.Repeat("y", 10)}}
	kit, err := reviewer.New(a).Prepare(context.Background(), work())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(kit.Instructions, "[Cut: the files included hold more than 262144 bytes.]") || strings.Contains(kit.Instructions, "two.md") {
		t.Errorf("the instructions end %q", kit.Instructions[len(kit.Instructions)-120:])
	}
}

// ran is what a tool's call answered: its output, and its failure, empty for none.
type ran struct {
	out, err string
}

// run runs the kit's tool name with the arguments args, a JSON object.
func run(t *testing.T, kit harness.Kit, name, args string) ran {
	t.Helper()
	for _, tool := range kit.Tools {
		if tool.Spec().Name == name {
			out, err := tool.Run(context.Background(), harness.Call{ID: "c", Name: name, Arguments: json.RawMessage(args)})
			if err != nil {
				return ran{out: out, err: err.Error()}
			}
			return ran{out: out}
		}
	}
	t.Fatalf("no tool %s", name)
	return ran{}
}

// failedAs checks that r failed as wantErr says, starting so, or didn't fail for an empty one, and reports whether
// r succeeded.
func failedAs(t *testing.T, r ran, wantErr string) bool {
	t.Helper()
	if !strings.HasPrefix(r.err, wantErr) || (r.err == "") != (wantErr == "") {
		t.Fatalf("the call failed with %q, want %q", r.err, wantErr)
	}
	return r.err == ""
}

// says checks that out says each of want, and none of unwanted.
func says(t *testing.T, out string, want, unwanted []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("%.2000s\n\ndoesn't say %q", out, w)
		}
	}
	for _, u := range unwanted {
		if strings.Contains(out, u) {
			t.Errorf("%.2000s\n\nsays %q", out, u)
		}
	}
}

func TestReadDiff(t *testing.T) {
	d := change("a.go", "b.go", "go.sum")
	for i := range 2000 {
		d.Files[1].Hunks[0].Lines = append(d.Files[1].Hunks[0].Lines, code.DiffLine{Kind: code.LineAdded, New: 5 + i, Text: strings.Repeat("z", 40)})
	}
	d.Files = append(d.Files, code.FileDiff{Status: code.FileRenamed, Path: "img/new.png", OldPath: "img/old.png", Binary: true})
	a := &api{diff: d, files: map[string]string{"cobranch-review.yaml": "paths:\n  ignore: [go.sum]\n"}}
	kit, err := reviewer.New(a).Prepare(context.Background(), work())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, args string
		want       []string
		unwanted   []string
		wantErr    string
	}{
		{name: "the first page", args: `{}`, want: []string{"Branch feature at aaaaaaaaaaaa against main: 3 files, +2 -2 lines. " +
			"main's /cobranch-review.yaml leaves 1 more out (`paths.ignore`).",
			"## a.go (modified, +1 -1)\n\n@@ -3,2 +3,2 @@ func main()\n     3      3   x := 1\n     4        - y := 2\n            4 + y := 3",
			"Files 0 to 0 of 3. Call read_diff with from 1 for the rest."}, unwanted: []string{"go.sum", "b.go"}},
		{name: "a file longer than a page", args: `{"from":1}`, want: []string{"## b.go (modified, +1 -1)", "  1000 + zzzz",
			"[Cut: this file's change is longer than a page holds", "Files 1 to 1 of 3. Call read_diff with from 2 for the rest."},
			unwanted: []string{"## a.go", "img/new.png"}},
		{name: "the last page", args: `{"from":2}`, want: []string{"## img/new.png (renamed, +0 -0), from img/old.png\n\nA binary file",
			"Files 2 to 2 of 3."}, unwanted: []string{"## a.go", "for the rest"}},
		{name: "a file past the last", args: `{"from":3}`, wantErr: "from is a file's number, from 0 to 2"},
		{name: "arguments that aren't an object", args: `[]`, wantErr: "read_diff takes an object of from"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := run(t, kit, "read_diff", tt.args)
			if !failedAs(t, r, tt.wantErr) {
				return
			}
			if len(r.out) > harness.MaxOutput {
				t.Errorf("the page is %d bytes, more than a call answers", len(r.out))
			}
			says(t, r.out, tt.want, tt.unwanted)
		})
	}
}

// submittedOnce checks that a was sent one review, want when it isn't nil, or none when !once.
func submittedOnce(t *testing.T, a *api, once bool, want *code.ReviewInput) {
	t.Helper()
	switch {
	case !once && len(a.inputs) != 0, once && len(a.inputs) != 1:
		t.Errorf("submitted %d reviews, want one: %v", len(a.inputs), once)
	case want != nil && !reflect.DeepEqual(a.inputs[0], *want):
		t.Errorf("submitted %+v, want %+v", a.inputs[0], *want)
	}
}

func TestSubmitReview(t *testing.T) {
	comments := `{"state":"changes_requested","body":"Two things.","comments":[{"path":"a.go","line":4,"body":"Off by one."},` +
		`{"path":"a.go","side":"old","line":3,"endLine":4,"body":"Keep this."}]}`
	tests := []struct {
		name, args string
		diff       func(*code.Diff)
		submitErr  error
		twice      bool
		want       string
		wantErr    string
		wantInput  *code.ReviewInput
	}{
		{name: "an approval", args: `{"state":"approved"}`, want: "Submitted your review of aaaaaaaaaaaa, approved, with 0 comments.",
			wantInput: &code.ReviewInput{State: code.ReviewApproved}},
		{name: "comments on both sides of the diff", args: comments, want: "Submitted your review of aaaaaaaaaaaa, changes_requested, with 2 comments.",
			wantInput: &code.ReviewInput{State: code.ReviewChangesRequested, Body: "Two things.", Branch: "feature", Comments: []code.ReviewComment{
				{Anchor: code.Anchor{SHA: head, Path: "a.go", StartLine: 4, EndLine: 4}, Body: "Off by one."},
				{Anchor: code.Anchor{SHA: base, Path: "a.go", StartLine: 3, EndLine: 4}, Body: "Keep this."}}}},
		{name: "a second review", args: `{"state":"approved"}`, twice: true, want: "You submitted your review of aaaaaaaaaaaa already: approved. Call finish now."},
		{name: "a side that isn't one", args: `{"state":"neutral","comments":[{"path":"a.go","side":"left","line":1,"body":"Hm."}]}`,
			wantErr: "comment 0's side is new or old"},
		{name: "the old side of a change that starts from nothing", args: comments, diff: func(d *code.Diff) { d.Base = "" },
			wantErr: "comment 1 is on the old side, but the change starts from nothing"},
		{name: "changes requested without saying what", args: `{"state":"changes_requested"}`,
			wantErr: "the review isn't one Cobranch takes\nbody: a review that requests changes or comments says what"},
		{name: "arguments that aren't an object", args: `"approved"`, wantErr: "submit_review takes an object of state, body and comments"},
		{name: "a refusal", args: `{"state":"approved"}`, submitErr: errors.New("the API answered 409, branch.deleted"),
			wantErr: "submitting the review: the API answered 409, branch.deleted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := &api{diff: change("a.go")}
			if tt.diff != nil {
				tt.diff(&a.diff)
			}
			kit, err := reviewer.New(a).Prepare(context.Background(), work())
			if err != nil {
				t.Fatal(err)
			}
			a.submitErr = tt.submitErr
			if tt.twice {
				failedAs(t, run(t, kit, "submit_review", tt.args), "")
			}
			r := run(t, kit, "submit_review", tt.args)
			if failedAs(t, r, tt.wantErr) && !strings.HasPrefix(r.out, tt.want) {
				t.Errorf("submit_review = %q, want %q", r.out, tt.want)
			}
			submittedOnce(t, a, tt.wantErr == "", tt.wantInput)
		})
	}
}
