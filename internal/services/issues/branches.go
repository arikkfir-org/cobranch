package issues

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// LinkedBy says how a branch came to work on its issue: by its name, which the push worker and the issue's creation
// link, or by hand, which nothing undoes.
type LinkedBy string

// How a branch is linked to its issue.
const (
	LinkedByName LinkedBy = "name"
	LinkedByHand LinkedBy = "hand"
)

// LinkedBranch is a branch linked to an issue, as the issue's page lists it: its repository, its name, its head and
// its status as a request to merge, empty until a push works it out, and whether it was deleted since.
type LinkedBranch struct {
	Repository Repository
	Name       string
	Head       string
	Status     string
	Deleted    bool
	LinkedBy   LinkedBy
	LinkedAt   time.Time
}

// Branch is a live branch of one of the organization's repositories, and its link: the issue it works on, if any, and
// how it was linked. A branch unlinked by hand has a link with no issue, which its name never replaces.
type Branch struct {
	ID         uuid.UUID
	OrgID      uuid.UUID
	Repository Repository
	Name       string
	// IssueID is the issue it works on; uuid.Nil for none.
	IssueID uuid.UUID
	// LinkedBy is how its link was made; "" while it has none.
	LinkedBy LinkedBy
}

// Link is the issue a branch works on, as the branch's page shows it, and how the branch was linked.
type Link struct {
	Issue    Issue
	LinkedBy LinkedBy
}

// EventBranch is the branch an issue.branch_linked or issue.branch_unlinked event names; its repository's slug and its
// name are blank for a viewer who doesn't read the repository.
type EventBranch struct {
	RepositoryID uuid.UUID
	Repository   string
	Name         string
	LinkedBy     LinkedBy
}

// ErrBranchNotFound is a branch that isn't one of the repository's live branches.
var ErrBranchNotFound = errors.New("no such branch")

// StartsFrom lists the states that linking a branch moves an issue out of, to In progress.
var StartsFrom = []State{StateBacklog, StateTodo}

// FinishesFrom lists the states that the merge of a branch linked to an issue moves it out of, to Done: every open
// one.
var FinishesFrom = []State{StateBacklog, StateTodo, StateInProgress, StateInReview}

// Named is the number of the issue a branch's name names under the organization's key: its first identifier that
// stands as a word of its own, in any case, bounded by the name's ends or by anything but a letter or a digit of any
// script, so acme-42-login, alice/ACME-42 and fix/acme-42_retry name 42, acme-420 names 420, and xacme-42 and caféacme-42
// none. The default branch names none.
func Named(name, key string) (int, bool) {
	if name == git.DefaultBranch || key == "" {
		return 0, false
	}
	pattern, err := regexp.Compile(`(?i)(?:^|[^\p{L}\p{N}])` + regexp.QuoteMeta(key) + `-([0-9]+)(?:$|[^\p{L}\p{N}])`)
	if err != nil {
		return 0, false // a key is letters and digits: it always compiles
	}
	m := pattern.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 1 || n > maxNumber {
		return 0, false
	}
	return n, true
}

// maxNumber is the highest number an issue can have.
const maxNumber = 1<<31 - 1

// Branches lists the branches linked to one of the organization's issues, of the repositories v reads; it takes
// issues.read.
func (s *Service) Branches(ctx context.Context, v Viewer, orgID uuid.UUID, number int) ([]LinkedBranch, error) {
	ctx, span := tracer.Start(ctx, "issues.Service.Branches")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()), attribute.Int("cobranch.issue.number", number))
	issue, err := s.get(ctx, v, orgID, number, PermRead)
	if err != nil {
		return nil, failed(span, "listing the issue's branches", err)
	}
	all, err := s.store.Branches(ctx, issue.ID)
	if err != nil {
		return nil, failed(span, "listing the issue's branches", err)
	}
	readable, err := s.readable(ctx, v, orgID)
	if err != nil {
		return nil, failed(span, "listing the issue's branches", err)
	}
	shown := make([]LinkedBranch, 0, len(all))
	for _, b := range all {
		if slices.Contains(readable, b.Repository.ID) {
			shown = append(shown, b)
		}
	}
	return shown, nil
}

// OfBranch is the issue a live branch of the organization's repository works on, for v: nil when it has none, when
// v holds no issues.read, since a branch shows its issue only to those who read issues, and when v doesn't read the
// repository or the branch doesn't live, as the branch's own page answers those.
func (s *Service) OfBranch(ctx context.Context, v Viewer, orgID, repoID uuid.UUID, name string) (*Link, error) {
	ctx, span := tracer.Start(ctx, "issues.Service.OfBranch")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.branch", name))
	if name == git.DefaultBranch {
		return nil, nil
	}
	held, err := s.authz.Holds(ctx, v.ID, auth.ScopeOrganization, orgID)
	if err != nil {
		return nil, failed(span, "reading the branch's issue", fmt.Errorf("reading the viewer's permissions: %w", err))
	}
	if !slices.Contains(held, PermRead) {
		return nil, nil
	}
	b, err := s.branch(ctx, v, orgID, repoID, name)
	if errors.Is(err, ErrBranchNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, failed(span, "reading the branch's issue", err)
	}
	if b.IssueID == uuid.Nil {
		return nil, nil
	}
	issue, err := s.store.ByID(ctx, b.IssueID)
	if err != nil {
		return nil, failed(span, "reading the branch's issue", wrap("finding the issue", err))
	}
	readable, err := s.readable(ctx, v, orgID)
	if err != nil {
		return nil, failed(span, "reading the branch's issue", err)
	}
	return &Link{Issue: shown(issue, readable), LinkedBy: b.LinkedBy}, nil
}

// branch finds the live branch of the organization's repository by name, which v reads, or ErrBranchNotFound.
func (s *Service) branch(ctx context.Context, v Viewer, orgID, repoID uuid.UUID, name string) (Branch, error) {
	held, err := s.authz.Holds(ctx, v.ID, auth.ScopeRepository, repoID)
	if err != nil {
		return Branch{}, fmt.Errorf("reading the viewer's permissions on the repository: %w", err)
	}
	if !slices.Contains(held, repos.PermRead) {
		return Branch{}, ErrBranchNotFound
	}
	b, err := s.store.Branch(ctx, orgID, repoID, name)
	if err != nil {
		return Branch{}, wrap("finding the branch", err)
	}
	return b, nil
}

// Link links a live branch of the organization's repository to one of its issues by hand, for v, in place of any
// issue it worked on, and answers the link, with the issue as the link left it; it takes issues.write, and repo.read on
// the repository. An issue in Backlog or Todo moves to In progress, for v. The default branch works on no issue.
func (s *Service) Link(ctx context.Context, v Viewer, orgID, repoID uuid.UUID, name string, number int) (Link, error) {
	ctx, span := tracer.Start(ctx, "issues.Service.Link")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.branch", name),
		attribute.Int("cobranch.issue.number", number))
	if err := s.require(ctx, v, orgID, PermWrite); err != nil {
		return Link{}, failed(span, "linking the branch", err)
	}
	if name == git.DefaultBranch {
		return Link{}, &orgs.ValidationError{Field: "branch", Code: "branch.default", Message: "the default branch works on no issue"}
	}
	b, err := s.branch(ctx, v, orgID, repoID, name)
	if err != nil {
		return Link{}, failed(span, "linking the branch", err)
	}
	issue, err := s.store.Get(ctx, orgID, number)
	if errors.Is(err, ErrNotFound) {
		return Link{}, &orgs.ValidationError{Field: "number", Code: "issue.invalid", Message: "not an issue of this organization"}
	}
	if err != nil {
		return Link{}, failed(span, "linking the branch", fmt.Errorf("finding the issue: %w", err))
	}
	how := b.LinkedBy
	if b.IssueID != issue.ID {
		if err := s.store.Link(ctx, v.ID, b, &issue.ID); err != nil {
			return Link{}, failed(span, "linking the branch", err)
		}
		how = LinkedByHand
	}
	if issue, err = s.find(ctx, v, orgID, number); err != nil {
		return Link{}, failed(span, "linking the branch", fmt.Errorf("reading the issue back: %w", err))
	}
	return Link{Issue: issue, LinkedBy: how}, nil
}

// Unlink unlinks a live branch of the organization's repository from its issue by hand, for v, so its name never links
// it again; it takes issues.write, and repo.read on the repository.
func (s *Service) Unlink(ctx context.Context, v Viewer, orgID, repoID uuid.UUID, name string) error {
	ctx, span := tracer.Start(ctx, "issues.Service.Unlink")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.branch", name))
	if err := s.require(ctx, v, orgID, PermWrite); err != nil {
		return failed(span, "unlinking the branch", err)
	}
	b, err := s.branch(ctx, v, orgID, repoID, name)
	if err != nil {
		return failed(span, "unlinking the branch", err)
	}
	if b.IssueID == uuid.Nil && b.LinkedBy == LinkedByHand {
		return nil
	}
	return failed(span, "unlinking the branch", s.store.Link(ctx, v.ID, b, nil))
}
