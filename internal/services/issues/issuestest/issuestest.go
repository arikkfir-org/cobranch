// Package issuestest holds a fake of the issues domain's port, for the tests of its service and of the adapters that
// serve it: a Store in memory over an orgstest.Store, which keeps the organizations, members and grants the authorizer
// reads.
package issuestest

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/issues"
	"cobranch.dev/internal/services/orgs/orgstest"
	"cobranch.dev/internal/services/repos"
)

// Store is an issues.Store in memory. Repos are the repositories issues may be about, by ID. Fail, when set, fails
// every write.
type Store struct {
	Orgs   *orgstest.Store
	Repos  map[uuid.UUID]repos.Repository
	Issues []issues.Issue
	Events map[uuid.UUID][]issues.Event
	last   map[uuid.UUID]int
	Fail   error
}

var _ issues.Store = (*Store)(nil)

// NewStore returns an empty Store over o.
func NewStore(o *orgstest.Store) *Store {
	return &Store{Orgs: o, Repos: map[uuid.UUID]repos.Repository{}, Events: map[uuid.UUID][]issues.Event{}, last: map[uuid.UUID]int{}}
}

// AddRepository makes repo one of its organization's, for the store and the authorizer.
func (s *Store) AddRepository(repo repos.Repository) {
	s.Repos[repo.ID], s.Orgs.Repos[repo.ID] = repo, repo.OrgID
}

// filled is issue with its organization's slug and key, and the names of who and what it refers to.
func (s *Store) filled(issue issues.Issue) issues.Issue {
	org := s.Orgs.Orgs[issue.OrgID]
	issue.OrgSlug, issue.Key = org.Slug, org.IssueKey
	for _, p := range []**issues.Principal{&issue.Assignee, &issue.CreatedBy} {
		if *p != nil {
			*p = &issues.Principal{ID: (*p).ID, DisplayName: s.Orgs.Names[(*p).ID]}
		}
	}
	if issue.Repository != nil {
		r := s.Repos[issue.Repository.ID]
		issue.Repository = &issues.Repository{ID: r.ID, Slug: r.Slug, Name: r.Name}
	}
	return issue
}

func (s *Store) event(actor, issueID uuid.UUID, kind string, changes []issues.Change) {
	s.Events[issueID] = append(s.Events[issueID], issues.Event{ID: uuid.New(), Kind: kind,
		Actor: &issues.Principal{ID: actor, DisplayName: s.Orgs.Names[actor]}, Changes: changes})
}

// Create numbers and records the issue.
func (s *Store) Create(_ context.Context, actor uuid.UUID, issue issues.Issue) (issues.Issue, error) {
	if s.Fail != nil {
		return issues.Issue{}, s.Fail
	}
	s.last[issue.OrgID]++
	issue.Number = s.last[issue.OrgID]
	s.Issues = append(s.Issues, issue)
	s.event(actor, issue.ID, "issue.created", nil)
	return s.filled(issue), nil
}

func (s *Store) index(orgID uuid.UUID, number int) int {
	return slices.IndexFunc(s.Issues, func(i issues.Issue) bool { return i.OrgID == orgID && i.Number == number })
}

// Get finds an issue by number.
func (s *Store) Get(_ context.Context, orgID uuid.UUID, number int) (issues.Issue, error) {
	i := s.index(orgID, number)
	if i < 0 {
		return issues.Issue{}, issues.ErrNotFound
	}
	return s.filled(s.Issues[i]), nil
}

// List lists the issues q matches, newest first.
func (s *Store) List(_ context.Context, orgID uuid.UUID, q issues.Query) ([]issues.Issue, bool, error) {
	var out []issues.Issue
	for _, i := range slices.Backward(s.Issues) {
		if i.OrgID == orgID && matches(i, q) {
			out = append(out, s.filled(i))
		}
	}
	slices.SortFunc(out, func(a, b issues.Issue) int { return b.Number - a.Number })
	if len(out) > q.Limit {
		return out[:q.Limit], true, nil
	}
	return out, false, nil
}

func matches(i issues.Issue, q issues.Query) bool {
	text := strings.ToLower(q.Text)
	switch {
	case !slices.Contains(q.States, i.State), q.Below != 0 && i.Number >= q.Below:
		return false
	case q.AssigneeID != nil && *q.AssigneeID != idOf(i.Assignee):
		return false
	case q.RepositoryID != nil && *q.RepositoryID != repoOf(i.Repository):
		return false
	}
	return text == "" || strings.Contains(strings.ToLower(i.Title), text) || strings.Contains(strings.ToLower(i.Description), text) ||
		i.Number == q.Number
}

func idOf(p *issues.Principal) uuid.UUID {
	if p == nil {
		return uuid.Nil
	}
	return p.ID
}

func repoOf(r *issues.Repository) uuid.UUID {
	if r == nil {
		return uuid.Nil
	}
	return r.ID
}

// Update writes the issue over the one at version.
func (s *Store) Update(_ context.Context, actor uuid.UUID, issue issues.Issue, version int64, changes []issues.Change) (issues.Issue, error) {
	if s.Fail != nil {
		return issues.Issue{}, s.Fail
	}
	i := s.index(issue.OrgID, issue.Number)
	switch {
	case i < 0:
		return issues.Issue{}, issues.ErrNotFound
	case s.Issues[i].Version != version:
		return issues.Issue{}, issues.ErrChanged
	}
	issue.Version = version + 1
	s.Issues[i] = issue
	s.event(actor, issue.ID, "issue.changed", changes)
	return s.filled(issue), nil
}

// Delete deletes the issue at version, with its events.
func (s *Store) Delete(_ context.Context, _ uuid.UUID, issue issues.Issue, version int64) error {
	if s.Fail != nil {
		return s.Fail
	}
	i := s.index(issue.OrgID, issue.Number)
	switch {
	case i < 0:
		return issues.ErrNotFound
	case s.Issues[i].Version != version:
		return issues.ErrChanged
	}
	s.Issues = slices.Delete(s.Issues, i, i+1)
	delete(s.Events, issue.ID)
	return nil
}

// Activity lists the issue's events.
func (s *Store) Activity(_ context.Context, issueID uuid.UUID) ([]issues.Event, error) {
	return slices.Clone(s.Events[issueID]), nil
}

// Member finds one of the organization's members.
func (s *Store) Member(_ context.Context, orgID, principalID uuid.UUID) (issues.Principal, error) {
	if !slices.Contains(s.Orgs.Memberships[orgID], principalID) {
		return issues.Principal{}, issues.ErrNotFound
	}
	return issues.Principal{ID: principalID, DisplayName: s.Orgs.Names[principalID]}, nil
}

// Repository finds one of the organization's repositories.
func (s *Store) Repository(_ context.Context, orgID, repoID uuid.UUID) (issues.Repository, error) {
	r, ok := s.Repos[repoID]
	if !ok || r.OrgID != orgID {
		return issues.Repository{}, issues.ErrNotFound
	}
	return issues.Repository{ID: r.ID, Slug: r.Slug, Name: r.Name}, nil
}
