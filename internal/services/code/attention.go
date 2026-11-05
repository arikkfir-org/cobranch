package code

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// RepoBranch is a branch with the repository it is of, by its ID and slug.
type RepoBranch struct {
	RepoID uuid.UUID
	Repo   string
	Branch
}

// Need is why a branch needs a viewer.
type Need string

// The needs, in the order Attention lists them.
const (
	// NeedFix is a Blocked branch whose head the viewer wrote: only they move it on.
	NeedFix Need = "fix"
	// NeedReview is a branch that waits for an approval the viewer may give, of a head they neither wrote nor
	// reviewed.
	NeedReview Need = "review"
	// NeedMerge is a Ready branch the viewer may merge.
	NeedMerge Need = "merge"
	// NeedWatch is a Processing branch whose head the viewer wrote: its jobs and reviews are still running.
	NeedWatch Need = "watch"
)

// needOrder ranks the needs as Attention lists them.
var needOrder = []Need{NeedFix, NeedReview, NeedMerge, NeedWatch}

// Attention is a branch that needs a viewer, and why.
type Attention struct {
	RepoBranch
	Need Need
}

// The most branches Attention weighs and lists.
const (
	attentionWeighed = 200
	attentionListed  = 20
)

// Attention lists the branches of the organization that need v, at most 20: those of the repositories v reads that
// are Blocked with a head v wrote, wait for an approval v may give (reviews.submit) of a head v neither wrote nor
// reviewed, are Ready and v may merge them (repo.merge), or are Processing with a head v wrote; by need in that order,
// then the latest moved first. It takes org.read, and answers orgs.ErrNotFound without it.
func (s *Service) Attention(ctx context.Context, v Viewer, orgID uuid.UUID) ([]Attention, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Attention")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()))
	held, err := s.authz.Holds(ctx, v.ID, auth.ScopeOrganization, orgID)
	if err != nil {
		return nil, failed(span, "listing what needs the viewer", fmt.Errorf("reading the viewer's permissions: %w", err))
	}
	if !slices.Contains(held, orgs.PermRead) {
		return nil, failed(span, "listing what needs the viewer", orgs.ErrNotFound)
	}
	may, err := s.repositoriesWith(ctx, v, orgID, repos.PermRead, repos.PermMerge, PermReview)
	if err != nil {
		return nil, failed(span, "listing what needs the viewer", err)
	}
	if len(may[repos.PermRead]) == 0 {
		return nil, nil
	}
	branches, err := s.store.OpenBranches(ctx, may[repos.PermRead], attentionWeighed)
	if err != nil {
		return nil, failed(span, "listing what needs the viewer", err)
	}
	out := make([]Attention, 0, len(branches))
	for _, b := range branches {
		if need, ok := needOf(v.ID, b, slices.Contains(may[repos.PermMerge], b.RepoID), slices.Contains(may[PermReview], b.RepoID)); ok {
			out = append(out, Attention{RepoBranch: b, Need: need})
		}
	}
	slices.SortStableFunc(out, func(a, b Attention) int {
		return cmp.Compare(slices.Index(needOrder, a.Need), slices.Index(needOrder, b.Need))
	})
	return out[:min(len(out), attentionListed)], nil
}

// repositoriesWith lists, for each of permissions, the organization's repositories where v holds it.
func (s *Service) repositoriesWith(ctx context.Context, v Viewer, orgID uuid.UUID, permissions ...string) (map[string][]uuid.UUID, error) {
	out := make(map[string][]uuid.UUID, len(permissions))
	for _, p := range permissions {
		ids, err := s.authz.Repositories(ctx, v.ID, orgID, p)
		if err != nil {
			return nil, fmt.Errorf("listing the repositories where the viewer holds %s: %w", p, err)
		}
		out[p] = ids
	}
	return out, nil
}

// needOf is why b needs viewer, who may merge it or review it as merge and review say, and false when it doesn't.
func needOf(viewer uuid.UUID, b RepoBranch, merge, review bool) (Need, bool) {
	wrote := b.Commit != nil && b.Commit.Author.Principal != nil && b.Commit.Author.Principal.ID == viewer
	switch {
	case b.Status == StatusBlocked && wrote:
		return NeedFix, true
	case review && !wrote && waitsForApproval(b) && !reviewed(viewer, b):
		return NeedReview, true
	case b.Status == StatusReady && merge:
		return NeedMerge, true
	case b.Status == StatusProcessing && wrote:
		return NeedWatch, true
	}
	return "", false
}

// waitsForApproval reports whether b's approvals check is unmet.
func waitsForApproval(b RepoBranch) bool {
	return slices.ContainsFunc(b.Checks, func(c Check) bool { return c.Kind == CheckApprovals && !c.Met })
}

// reviewed reports whether viewer reviewed b's head.
func reviewed(viewer uuid.UUID, b RepoBranch) bool {
	return b.Commit != nil && slices.ContainsFunc(b.Commit.Reviews, func(r Review) bool { return r.Reviewer != nil && r.Reviewer.ID == viewer })
}
