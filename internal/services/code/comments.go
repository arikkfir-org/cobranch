package code

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// Anchor points at lines StartLine to EndLine, from 1, of the file at Path of commit SHA: a chat thread's, which stays
// there when the branch moves on, and so a review comment's
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/reviews.md, "Comments").
type Anchor struct {
	SHA       string
	Path      string
	StartLine int
	EndLine   int
}

// MaxMessageBody is the longest message of a chat, and so of a review comment, in characters.
const MaxMessageBody = 20000

// MaxReviewComments is the most comments a review carries.
const MaxReviewComments = 100

// maxAnchorPath is the longest path an anchor names, in bytes.
const maxAnchorPath = 4096

// ReviewComment is a comment of a review on lines of a file, which opens a thread anchored there in the branch's chat,
// its first message Body.
type ReviewComment struct {
	Anchor Anchor
	Body   string
}

// ValidateMessage reports whether body is a chat's message: not blank, and at most MaxMessageBody characters of UTF-8;
// field names what the caller gave it in.
func ValidateMessage(field, body string) error {
	switch {
	case strings.TrimSpace(body) == "":
		return &orgs.ValidationError{Field: field, Code: "body.empty", Message: "a message says something"}
	case !utf8.ValidString(body):
		return &orgs.ValidationError{Field: field, Code: "body.invalid", Message: "a message is UTF-8 text"}
	case utf8.RuneCountInString(body) > MaxMessageBody:
		return &orgs.ValidationError{Field: field, Code: "body.too_long", Message: fmt.Sprintf("a message is at most %d characters", MaxMessageBody)}
	}
	return nil
}

// ValidateAnchor reports whether a points at lines of a file at a commit: the commit's full name, a path relative to
// the root as names separated by /, without . or .. and at most 4096 bytes, and lines from 1, the last at or after
// the first; field names what the caller gave it in.
func ValidateAnchor(field string, a Anchor) error {
	invalid := func(name, message string) error {
		return &orgs.ValidationError{Field: field + "." + name, Code: "anchor.invalid", Message: message}
	}
	switch {
	case !shaPattern.MatchString(a.SHA):
		return invalid("sha", "an anchor names its commit by its full name, in lowercase hexadecimal")
	case a.Path == "", len(a.Path) > maxAnchorPath, path.Clean(a.Path) != a.Path, strings.HasPrefix(a.Path, "/"), a.Path == ".",
		a.Path == "..", strings.HasPrefix(a.Path, "../"), !utf8.ValidString(a.Path):
		return invalid("path", "an anchor names a file by its path from the root, as names separated by /")
	case a.StartLine < 1:
		return invalid("startLine", "an anchor's lines start at 1")
	case a.EndLine < a.StartLine:
		return invalid("endLine", "an anchor's last line is at or after its first")
	case a.EndLine > math.MaxInt32:
		return invalid("endLine", fmt.Sprintf("an anchor's lines end by %d", math.MaxInt32))
	}
	return nil
}

// validateComments reports whether comments are a review's: at most MaxReviewComments, each anchored and saying
// something, and a branch to open them in when there are any.
func validateComments(branch string, comments []ReviewComment) error {
	if len(comments) > MaxReviewComments {
		return &orgs.ValidationError{Field: "comments", Code: "comments.too_many", Message: fmt.Sprintf("a review carries at most %d comments", MaxReviewComments)}
	}
	if len(comments) > 0 && branch == "" {
		return &orgs.ValidationError{Field: "branch", Code: "branch.required", Message: "a review's comments open threads in a branch's chat"}
	}
	for i, c := range comments {
		field := fmt.Sprintf("comments[%d]", i)
		if err := ValidateAnchor(field+".anchor", c.Anchor); err != nil {
			return err
		}
		if err := ValidateMessage(field+".body", c.Body); err != nil {
			return err
		}
	}
	return nil
}

// checkComments reports whether in's comments open their threads in the chat of a live branch of the repository,
// anchored at commits it holds: branch.unknown, or a comment's anchor.commit_not_found, otherwise.
func (s *Service) checkComments(ctx context.Context, repoID uuid.UUID, in ReviewInput) error {
	if len(in.Comments) == 0 {
		return nil
	}
	if _, err := s.store.Branch(ctx, repoID, in.Branch); errors.Is(err, repos.ErrNotFound) {
		return &orgs.ValidationError{Field: "branch", Code: "branch.unknown", Message: "the repository has no such branch"}
	} else if err != nil {
		return fmt.Errorf("finding branch %s: %w", in.Branch, err)
	}
	var shas []string
	for _, c := range in.Comments {
		if !slices.Contains(shas, c.Anchor.SHA) {
			shas = append(shas, c.Anchor.SHA)
		}
	}
	held, err := s.store.CommitsAt(ctx, repoID, shas)
	if err != nil {
		return fmt.Errorf("finding the comments' commits: %w", err)
	}
	for i, c := range in.Comments {
		if _, found := held[c.Anchor.SHA]; !found {
			return &orgs.ValidationError{Field: fmt.Sprintf("comments[%d].anchor.sha", i), Code: "anchor.commit_not_found",
				Message: "the repository holds no such commit"}
		}
	}
	return nil
}
