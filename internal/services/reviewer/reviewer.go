// Package reviewer fits the agent harness to the AI reviewer's review tasks
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/reviews.md, "The AI reviewer"). Before the
// model is asked, it reads main's /cobranch-review.yaml and the branch's diff through Cobranch's API, as the task's own
// token; it reviews a change it may not read itself, with a neutral review that says why, and otherwise gives the model
// the repository's instructions and two tools: one reads the diff, the other submits the one review a task writes.
package reviewer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/harness"
)

var tracer = otel.Tracer("cobranch.dev/internal/services/reviewer")

// Timeout is the longest a review task runs from its start, after which it fails and leaves no review.
const Timeout = 30 * time.Minute

// ErrNotFound is what API answers for a branch, file or directory that isn't there.
var ErrNotFound = errors.New("not found")

// API is Cobranch's API as a review task's token reaches it.
type API interface {
	// BranchDiff answers the branch's change as a request to merge into its base.
	BranchDiff(ctx context.Context, org, repo, branch string) (code.Diff, error)
	// File answers the file at path of the commit ref names.
	File(ctx context.Context, org, repo, ref, path string) (code.File, error)
	// Tree answers the directory at path of the commit ref names; "" for the root.
	Tree(ctx context.Context, org, repo, ref, path string) (code.Tree, error)
	// Reviews answers every review of the commit, in the order they were submitted.
	Reviews(ctx context.Context, org, repo, sha string) ([]code.Review, error)
	// SubmitReview submits the token's review of the commit.
	SubmitReview(ctx context.Context, org, repo, sha string, in code.ReviewInput) (code.Review, error)
}

// Reviewer prepares the AI reviewer's review tasks: the harness's Kind for agents.KindReview.
type Reviewer struct {
	api API
}

var _ harness.Kind = (*Reviewer)(nil)

// New returns a Reviewer that reads and reviews through api.
func New(api API) *Reviewer {
	return &Reviewer{api: api}
}

// Prepare readies the review task w (harness.Kind): it ends one that already submitted its review, one whose settings
// or branch moved on, and one whose change it may not read, with a neutral review; and gives any other the
// repository's instructions, the tools and the timeout.
func (r *Reviewer) Prepare(ctx context.Context, w agents.Work) (harness.Kit, error) {
	t := w.Task
	ctx, span := tracer.Start(ctx, "reviewer.Reviewer.Prepare", trace.WithAttributes(attribute.String("cobranch.org.slug", w.OrgSlug),
		attribute.Int64("cobranch.task.number", t.Number), attribute.String("cobranch.repository.slug", t.RepoSlug),
		attribute.String("cobranch.branch", t.Branch), attribute.String("cobranch.commit", t.SHA)))
	defer span.End()
	if t.RepoSlug == "" || t.Branch == "" || t.SHA == "" {
		return ending(agents.TaskFailed, "The review task names no repository, branch and commit to review."), nil
	}
	rv := &review{api: r.api, org: w.OrgSlug, task: t}
	if done, err := rv.submitted(ctx); err != nil || done != nil {
		return reviewed(done), failed(span, err)
	}
	settings, end, err := rv.settings(ctx)
	if err != nil || end != nil {
		return orEnd(end), failed(span, err)
	}
	if end, err = rv.read(ctx, settings); err != nil || end != nil {
		return orEnd(end), failed(span, err)
	}
	instructions, err := rv.instructions(ctx, settings)
	if err != nil {
		return harness.Kit{}, failed(span, err)
	}
	return harness.Kit{Tools: []harness.Tool{readDiff{rv}, submitReview{rv}}, Instructions: instructions, Unfinished: rv.unfinished,
		Timeout: Timeout}, nil
}

// review is the review task the harness works: its organization and task, and once read, the change it reviews with
// the files main's /cobranch-review.yaml leaves out of it.
type review struct {
	api     API
	org     string
	task    agents.Task
	diff    code.Diff
	ignored int
}

// submitted answers the review the task submitted, nil before it did: the reviewer's latest of the commit since the
// task was created. A task that a newer one supersedes is cancelled in the transaction that creates the newer, and an
// ended task's token is refused, so no other task's review falls in that time.
func (rv *review) submitted(ctx context.Context) (*code.Review, error) {
	reviews, err := rv.api.Reviews(ctx, rv.org, rv.task.RepoSlug, rv.task.SHA)
	if err != nil {
		return nil, fmt.Errorf("reading the commit's reviews: %w", err)
	}
	for i := len(reviews) - 1; i >= 0; i-- {
		if r := reviews[i]; r.Reviewer != nil && r.Reviewer.ID == rv.task.Agent.ID && !r.SubmittedAt.Before(rv.task.CreatedAt) {
			return &r, nil
		}
	}
	return nil, nil
}

// unfinished says what the task has left to do before it may succeed: submit its review (harness.Kit).
func (rv *review) unfinished(ctx context.Context) (string, error) {
	done, err := rv.submitted(ctx)
	if err != nil || done != nil {
		return "", err
	}
	return "submit your review of " + short(rv.task.SHA) + " with " + submitName + " first", nil
}

// reviewed is the kit of a task that already submitted done; an empty one for nil.
func reviewed(done *code.Review) harness.Kit {
	if done == nil {
		return harness.Kit{}
	}
	return ending(agents.TaskSucceeded, "Reviewed "+short(done.SHA)+": "+string(done.State)+".")
}

// ending is the kit of a task that ends as state says, for why: its summary when it succeeds, its reason otherwise.
func ending(state agents.TaskState, why string) harness.Kit {
	change := agents.StateChange{State: state, Summary: why}
	if state != agents.TaskSucceeded {
		change = agents.StateChange{State: state, Reason: cut(why, agents.MaxReason)}
	}
	return harness.Kit{End: &change}
}

// orEnd is end, or an empty kit for nil.
func orEnd(end *harness.Kit) harness.Kit {
	if end == nil {
		return harness.Kit{}
	}
	return *end
}

// short is a commit's name as a reader recognizes it: its first 12 characters.
func short(sha string) string {
	return sha[:min(len(sha), 12)]
}

// cut answers at most the first n characters of s.
func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// failed records err, if any, on span, and returns it.
func failed(span trace.Span, err error) error {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}
