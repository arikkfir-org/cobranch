package code

import (
	"context"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// ReviewState is a review's verdict on a commit
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/reviews.md, "Reviews").
type ReviewState string

// The three states of a review.
const (
	// ReviewApproved approves the commit: it counts towards a branch's required approvals when a person submits it.
	ReviewApproved ReviewState = "approved"
	// ReviewChangesRequested asks for changes, which blocks a branch whose base requires reviews until its reviewer
	// submits another.
	ReviewChangesRequested ReviewState = "changes_requested"
	// ReviewNeutral comments without a verdict, and takes back its reviewer's earlier one.
	ReviewNeutral ReviewState = "neutral"
)

// ReviewStates lists the states a review takes.
var ReviewStates = []ReviewState{ReviewApproved, ReviewChangesRequested, ReviewNeutral}

// PermReview submits reviews.
const PermReview = "reviews.submit"

// PermPostMessages posts messages to the branches' chats, which a review's comments are: it is the chats' own
// permission, which a review with comments takes too.
const PermPostMessages = "messages.post"

// MaxReviewBody is the longest body a review takes, in characters.
const MaxReviewBody = 65536

// Review is a principal's verdict on a commit, with what they wrote: it belongs to the commit, so every branch at that
// commit shows it. A review is never changed: its reviewer submits another, and their latest on the commit is the one
// that counts.
type Review struct {
	ID    uuid.UUID
	SHA   string
	State ReviewState
	Body  string
	// HTML is Body rendered as Markdown, its relative links leading to the files of the commit; empty when it has no
	// body, or the service no Renderer.
	HTML string
	// Reviewer is who submitted it; nil once they are gone, when it counts no more.
	Reviewer *Principal
	// Person reports whether its reviewer is a person: only people's approvals count towards a branch's required
	// approvals, not agents' or API keys'.
	Person      bool
	SubmittedAt time.Time
}

// ReviewInput is the review a principal submits.
type ReviewInput struct {
	State ReviewState
	Body  string
	// Branch names the branch in whose chat Comments open their threads; a review without comments needs none.
	Branch string
	// Comments are the review's comments on lines of files, each a thread of the branch's chat.
	Comments []ReviewComment
}

// ValidateReview reports whether in is a review: one of the three states, a body of at most MaxReviewBody
// characters, which only an approval may leave blank unless comments say what, and at most MaxReviewComments
// comments, each anchored and saying something, with the branch they open their threads in.
func ValidateReview(in ReviewInput) error {
	if !slices.Contains(ReviewStates, in.State) {
		return &orgs.ValidationError{Field: "state", Code: "state.invalid", Message: "approved, changes_requested or neutral"}
	}
	if !utf8.ValidString(in.Body) || utf8.RuneCountInString(in.Body) > MaxReviewBody {
		return &orgs.ValidationError{Field: "body", Code: "body.invalid", Message: "at most 65,536 characters"}
	}
	if in.State != ReviewApproved && strings.TrimSpace(in.Body) == "" && len(in.Comments) == 0 {
		return &orgs.ValidationError{Field: "body", Code: "body.required", Message: "a review that requests changes or comments says what, in its body or its comments"}
	}
	return validateComments(in.Branch, in.Comments)
}

// Latest is each reviewer's latest review among reviews, which are in the order they were submitted: the ones that
// count, in the order their reviewers first reviewed. A review whose reviewer is gone counts no more.
func Latest(reviews []Review) []Review {
	var out []Review
	for _, r := range reviews {
		if r.Reviewer == nil {
			continue
		}
		if i := slices.IndexFunc(out, func(o Review) bool { return o.Reviewer.ID == r.Reviewer.ID }); i >= 0 {
			out[i] = r
			continue
		}
		out = append(out, r)
	}
	return out
}

// ReviewQuery is the page of a commit's reviews a listing asks for: from Cursor, as the previous page's Next names it,
// or else from the first; at most Limit, or DefaultPageSize when it is 0.
type ReviewQuery struct {
	Cursor string
	Limit  int
}

// ReviewPage is a page of a commit's reviews, in the order they were submitted, and the cursor of the next page, empty
// after the last.
type ReviewPage struct {
	Reviews []Review
	Next    string
}

// Reviews lists a page of the reviews on one of the repository's commits, in the order they were submitted; it takes
// repo.read. A commit the repository doesn't hold is repos.ErrNotFound, and a cursor that starts no page of its
// reviews is cursor.invalid.
func (s *Service) Reviews(ctx context.Context, v Viewer, repoID uuid.UUID, sha string, q ReviewQuery) (ReviewPage, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Reviews")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.commit.sha", sha))
	if err := s.read(ctx, v, repoID); err != nil {
		return ReviewPage{}, failed(span, "reading the reviews", err)
	}
	if err := ValidateSHA("sha", sha); err != nil {
		return ReviewPage{}, err
	}
	size, err := pageSize(q.Limit)
	if err != nil {
		return ReviewPage{}, err
	}
	after, err := decodeReviewCursor(q.Cursor)
	if err != nil {
		return ReviewPage{}, err
	}
	list, err := s.store.Reviews(ctx, repoID, sha, after, size+1)
	switch {
	case err != nil:
		return ReviewPage{}, failed(span, "reading the reviews", err)
	case len(list) == 0 && q.Cursor != "":
		return ReviewPage{}, invalidReviewCursor()
	}
	page := ReviewPage{Reviews: list}
	if len(list) > size {
		page.Reviews, page.Next = list[:size], encodeReviewCursor(list[size-1].ID)
	}
	if err := s.renderReviews(ctx, repoID, page.Reviews); err != nil {
		return ReviewPage{}, failed(span, "reading the reviews", err)
	}
	return page, nil
}

// encodeReviewCursor is the cursor of the page of a commit's reviews after the review named id, opaque to callers.
func encodeReviewCursor(id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString(id[:])
}

// decodeReviewCursor reads a cursor encodeReviewCursor made, uuid.Nil for none, or answers cursor.invalid.
func decodeReviewCursor(cursor string) (uuid.UUID, error) {
	if cursor == "" {
		return uuid.Nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return uuid.Nil, invalidReviewCursor()
	}
	id, err := uuid.FromBytes(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, invalidReviewCursor()
	}
	return id, nil
}

// invalidReviewCursor is the error of a cursor that starts no page of a commit's reviews.
func invalidReviewCursor() error {
	return &orgs.ValidationError{Field: "cursor", Code: "cursor.invalid", Message: "no page of this commit's reviews starts here"}
}

// Review finds one of the repository's reviews by its ID; it takes repo.read. A review the repository doesn't hold is
// repos.ErrNotFound.
func (s *Service) Review(ctx context.Context, v Viewer, repoID, id uuid.UUID) (Review, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Review")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.review.id", id.String()))
	if err := s.read(ctx, v, repoID); err != nil {
		return Review{}, failed(span, "reading the review", err)
	}
	r, err := s.store.Review(ctx, repoID, id)
	if err == nil {
		r, err = s.renderReview(ctx, repoID, r)
	}
	if err != nil {
		return Review{}, failed(span, "reading the review", err)
	}
	return r, nil
}

// SubmitReview submits v's review of one of the repository's commits, which works out again the status of the branches
// at it, and opens its comments as threads of the chat of the branch it names; it takes reviews.submit, and
// messages.post with comments. A commit the repository doesn't hold is repos.ErrNotFound; a branch it doesn't have,
// or that was deleted, is branch.unknown, and a comment's commit it doesn't hold comments[i].anchor.sha's
// anchor.commit_not_found.
func (s *Service) SubmitReview(ctx context.Context, v Viewer, repoID uuid.UUID, sha string, in ReviewInput) (Review, error) {
	ctx, span := tracer.Start(ctx, "code.Service.SubmitReview")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.commit.sha", sha),
		attribute.String("cobranch.review.state", string(in.State)))
	held, err := s.holds(ctx, v, repoID)
	if err != nil {
		return Review{}, failed(span, "submitting the review", err)
	}
	for _, perm := range permsToReview(in) {
		if !slices.Contains(held, perm) {
			return Review{}, fmt.Errorf("%w: %s on the repository", auth.ErrForbidden, perm)
		}
	}
	if err := ValidateSHA("sha", sha); err != nil {
		return Review{}, err
	}
	if err := ValidateReview(in); err != nil {
		return Review{}, err
	}
	if err := s.checkComments(ctx, repoID, in); err != nil {
		return Review{}, failed(span, "submitting the review", err)
	}
	// Rendered first, so a failure to render leaves nothing submitted, as every failure does.
	rendered, err := s.renderReview(ctx, repoID, Review{SHA: sha, Body: in.Body})
	if err != nil {
		return Review{}, failed(span, "submitting the review", err)
	}
	r, err := s.store.SubmitReview(ctx, v.ID, repoID, sha, in)
	if err != nil {
		return Review{}, failed(span, "submitting the review", err)
	}
	r.HTML = rendered.HTML
	return r, nil
}

// permsToReview lists the permissions submitting in takes: reviews.submit, and messages.post when it has comments.
func permsToReview(in ReviewInput) []string {
	if len(in.Comments) > 0 {
		return []string{PermReview, PermPostMessages}
	}
	return []string{PermReview}
}

// renderReview is r with its body rendered as Markdown, as renderReviews renders it.
func (s *Service) renderReview(ctx context.Context, repoID uuid.UUID, r Review) (Review, error) {
	one := []Review{r}
	err := s.renderReviews(ctx, repoID, one)
	return one[0], err
}

// renderReviews renders the bodies of the reviews in each of groups, of the repository's commits, as Markdown, in
// place: their relative links lead to the files of the commit each reviewed.
func (s *Service) renderReviews(ctx context.Context, repoID uuid.UUID, groups ...[]Review) error {
	said := func(reviews []Review) bool {
		return slices.ContainsFunc(reviews, func(r Review) bool { return r.Body != "" })
	}
	if s.markdown == nil || !slices.ContainsFunc(groups, said) {
		return nil
	}
	org, repo, err := s.store.Slugs(ctx, repoID)
	if err != nil {
		return fmt.Errorf("reading where the reviews are: %w", err)
	}
	for _, reviews := range groups {
		for i, r := range reviews {
			if r.Body == "" {
				continue
			}
			if reviews[i].HTML, err = s.markdown.Render(ctx, []byte(r.Body), Document{Org: org, Repo: repo, Ref: r.SHA, Commit: r.SHA}); err != nil {
				return fmt.Errorf("rendering a review of %s: %w", r.SHA, err)
			}
		}
	}
	return nil
}

// renderCommits renders the bodies of the reviews of commits, the repository's, as renderReviews does.
func (s *Service) renderCommits(ctx context.Context, repoID uuid.UUID, commits []Commit) error {
	groups := make([][]Review, 0, len(commits))
	for _, c := range commits {
		groups = append(groups, c.Reviews)
	}
	return s.renderReviews(ctx, repoID, groups...)
}
