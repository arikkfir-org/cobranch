// Package issuestest holds a fake of the issues domain's port, for the tests of its service and of the adapters that
// serve it: a Store in memory over an orgstest.Store, which keeps the organizations, members and grants the authorizer
// reads.
package issuestest

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/issues"
	"cobranch.dev/internal/services/orgs/orgstest"
	"cobranch.dev/internal/services/repos"
)

// Store is an issues.Store in memory. Repos are the repositories issues may be about, by ID, Live their live branches,
// by ID, each with its link, which an issue shows the newest of, and Labels the organizations' labels, by ID. Agents are the organizations' agents, whom
// issues may be assigned to too, by ID, each with its organization, and TaskOf, when set, finds the task an agent works
// on an issue in. Fail, when set, fails every write.
type Store struct {
	Orgs   *orgstest.Store
	Repos  map[uuid.UUID]repos.Repository
	Issues []issues.Issue
	Events map[uuid.UUID][]issues.Event
	Live   map[uuid.UUID]issues.Branch
	Kept   map[uuid.UUID]issues.Label
	Agents map[uuid.UUID]uuid.UUID
	TaskOf func(issueID, agentID uuid.UUID) *issues.Task
	last   map[uuid.UUID]int
	// linked orders the live branches' links, by branch ID, the higher the newer, up to links.
	linked map[uuid.UUID]int
	links  int
	Fail   error
}

var _ issues.Store = (*Store)(nil)

// NewStore returns an empty Store over o.
func NewStore(o *orgstest.Store) *Store {
	return &Store{Orgs: o, Repos: map[uuid.UUID]repos.Repository{}, Events: map[uuid.UUID][]issues.Event{},
		Live: map[uuid.UUID]issues.Branch{}, Kept: map[uuid.UUID]issues.Label{}, Agents: map[uuid.UUID]uuid.UUID{}, last: map[uuid.UUID]int{}, linked: map[uuid.UUID]int{}}
}

// TasksOf is a TaskOf over the tasks kept answers, oldest first, as an agents' fake keeps them: the newest task the
// agent works on the issue in.
func TasksOf(kept func() []agents.Task) func(issueID, agentID uuid.UUID) *issues.Task {
	return func(issueID, agentID uuid.UUID) *issues.Task {
		for _, t := range slices.Backward(kept()) {
			if t.IssueID == issueID && t.Agent.ID == agentID {
				return &issues.Task{Number: t.Number, State: t.State, RepoID: t.RepoID}
			}
		}
		return nil
	}
}

// Named answers the key and number that name the issue of id, as an agents' fake's IssueOf takes them; "" and 0 for
// none.
func (s *Store) Named(id uuid.UUID) (string, int64) {
	for _, i := range s.Issues {
		if i.ID == id {
			return s.Orgs.Orgs[i.OrgID].IssueKey, int64(i.Number)
		}
	}
	return "", 0
}

// AddRepository makes repo one of its organization's, for the store and the authorizer.
func (s *Store) AddRepository(repo repos.Repository) {
	s.Repos[repo.ID], s.Orgs.Repos[repo.ID] = repo, repo.OrgID
}

// AddBranch makes a live branch of the repository, with no link, and returns it.
func (s *Store) AddBranch(repoID uuid.UUID, name string) issues.Branch {
	r := s.Repos[repoID]
	b := issues.Branch{ID: uuid.New(), OrgID: r.OrgID, Repository: issues.Repository{ID: r.ID, Slug: r.Slug, Name: r.Name}, Name: name}
	s.Live[b.ID] = b
	return b
}

// filled is issue with its organization's slug and key, and the names of who and what it refers to.
func (s *Store) filled(issue issues.Issue) issues.Issue {
	org := s.Orgs.Orgs[issue.OrgID]
	issue.OrgSlug, issue.Key = org.Slug, org.IssueKey
	for _, p := range []**issues.Principal{&issue.Assignee, &issue.CreatedBy} {
		if *p != nil {
			*p = &issues.Principal{ID: (*p).ID, DisplayName: s.Orgs.Names[(*p).ID], Agent: s.Agents[(*p).ID] != uuid.Nil}
		}
	}
	issue.Task = nil
	if a := issue.Assignee; a != nil && a.Agent && s.TaskOf != nil {
		issue.Task = s.TaskOf(issue.ID, a.ID)
	}
	if issue.Repository != nil {
		r := s.Repos[issue.Repository.ID]
		issue.Repository = &issues.Repository{ID: r.ID, Slug: r.Slug, Name: r.Name}
	}
	issue.Branch = s.lastBranch(issue.ID)
	var labels []issues.IssueLabel
	for _, l := range issue.Labels {
		if kept, ok := s.Kept[l.ID]; ok {
			labels = append(labels, issues.IssueLabel{ID: kept.ID, Name: kept.Name, Color: kept.Color})
		}
	}
	slices.SortFunc(labels, func(a, b issues.IssueLabel) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	issue.Labels = labels
	return issue
}

func (s *Store) event(actor, issueID uuid.UUID, kind string, changes []issues.Change) {
	s.Events[issueID] = append(s.Events[issueID], issues.Event{ID: uuid.New(), Kind: kind,
		Actor: &issues.Principal{ID: actor, DisplayName: s.Orgs.Names[actor]}, Changes: changes})
}

func (s *Store) linkEvent(actor, issueID uuid.UUID, kind string, b issues.Branch) {
	s.Events[issueID] = append(s.Events[issueID], issues.Event{ID: uuid.New(), Kind: kind,
		Actor:  &issues.Principal{ID: actor, DisplayName: s.Orgs.Names[actor]},
		Branch: &issues.EventBranch{RepositoryID: b.Repository.ID, Repository: b.Repository.Slug, Name: b.Name, LinkedBy: issues.LinkedByHand}})
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

// Counts counts the organization's issues that q matches in any state and on any page: open, then closed.
func (s *Store) Counts(_ context.Context, orgID uuid.UUID, q issues.Query) (int, int, error) {
	q.States, q.Below = issues.States, 0
	var open, closed int
	for _, i := range s.Issues {
		switch {
		case i.OrgID != orgID || !matches(i, q):
		case slices.Contains(issues.OpenStates, i.State):
			open++
		default:
			closed++
		}
	}
	return open, closed, nil
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
	for _, id := range q.LabelIDs {
		if !slices.ContainsFunc(i.Labels, func(l issues.IssueLabel) bool { return l.ID == id }) {
			return false
		}
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

// Assignee finds one of the organization's members or agents.
func (s *Store) Assignee(_ context.Context, orgID, principalID uuid.UUID) (issues.Principal, error) {
	agent := s.Agents[principalID] == orgID
	if !agent && !slices.Contains(s.Orgs.Memberships[orgID], principalID) {
		return issues.Principal{}, issues.ErrNotFound
	}
	return issues.Principal{ID: principalID, DisplayName: s.Orgs.Names[principalID], Agent: agent}, nil
}

// Transact runs fn, and puts the issues back as they were when it fails.
func (s *Store) Transact(ctx context.Context, _ uuid.UUID, fn func(ctx context.Context) error) error {
	kept := slices.Clone(s.Issues)
	if err := fn(ctx); err != nil {
		s.Issues = kept
		return err
	}
	return nil
}

// Repository finds one of the organization's repositories.
func (s *Store) Repository(_ context.Context, orgID, repoID uuid.UUID) (issues.Repository, error) {
	r, ok := s.Repos[repoID]
	if !ok || r.OrgID != orgID {
		return issues.Repository{}, issues.ErrNotFound
	}
	return issues.Repository{ID: r.ID, Slug: r.Slug, Name: r.Name}, nil
}

// ByID finds an issue by its ID.
func (s *Store) ByID(_ context.Context, issueID uuid.UUID) (issues.Issue, error) {
	i := slices.IndexFunc(s.Issues, func(i issues.Issue) bool { return i.ID == issueID })
	if i < 0 {
		return issues.Issue{}, issues.ErrNotFound
	}
	return s.filled(s.Issues[i]), nil
}

// Branches lists the branches linked to the issue, by repository and name.
func (s *Store) Branches(_ context.Context, issueID uuid.UUID) ([]issues.LinkedBranch, error) {
	var out []issues.LinkedBranch
	for _, b := range s.Live {
		if b.IssueID == issueID {
			out = append(out, issues.LinkedBranch{Repository: b.Repository, Name: b.Name, LinkedBy: b.LinkedBy})
		}
	}
	slices.SortFunc(out, func(a, b issues.LinkedBranch) int {
		return strings.Compare(a.Repository.Slug+"/"+a.Name, b.Repository.Slug+"/"+b.Name)
	})
	return out, nil
}

// lastBranch is the live branch linked to the issue last, as an issue shows it; nil for none.
func (s *Store) lastBranch(issueID uuid.UUID) *issues.LiveBranch {
	var last *issues.Branch
	for _, b := range s.Live {
		if b.IssueID == issueID && (last == nil || s.linked[b.ID] > s.linked[last.ID] || s.linked[b.ID] == s.linked[last.ID] && b.Name < last.Name) {
			last = &b
		}
	}
	if last == nil {
		return nil
	}
	return &issues.LiveBranch{Repository: issues.Repository{ID: last.Repository.ID, Slug: last.Repository.Slug}, Name: last.Name}
}

// Branch finds a live branch by name.
func (s *Store) Branch(_ context.Context, orgID, repoID uuid.UUID, name string) (issues.Branch, error) {
	for _, b := range s.Live {
		if b.OrgID == orgID && b.Repository.ID == repoID && b.Name == name {
			return b, nil
		}
	}
	return issues.Branch{}, issues.ErrBranchNotFound
}

// Link links the branch by hand, or unlinks it for nil, and starts the issue it links.
func (s *Store) Link(_ context.Context, actor uuid.UUID, branch issues.Branch, issueID *uuid.UUID) error {
	if s.Fail != nil {
		return s.Fail
	}
	cur, ok := s.Live[branch.ID]
	if !ok {
		return issues.ErrBranchNotFound
	}
	to := uuid.Nil
	if issueID != nil {
		to = *issueID
	}
	if cur.IssueID != uuid.Nil && cur.IssueID != to {
		s.linkEvent(actor, cur.IssueID, "issue.branch_unlinked", cur)
	}
	was := cur.IssueID
	cur.IssueID, cur.LinkedBy = to, issues.LinkedByHand
	s.Live[branch.ID] = cur
	if was != to {
		s.links++
		s.linked[branch.ID] = s.links
	}
	if to == uuid.Nil || was == to {
		return nil
	}
	s.linkEvent(actor, to, "issue.branch_linked", cur)
	i := slices.IndexFunc(s.Issues, func(i issues.Issue) bool { return i.ID == to })
	if i >= 0 && slices.Contains(issues.StartsFrom, s.Issues[i].State) {
		from := s.Issues[i].State
		s.Issues[i].State, s.Issues[i].Version = issues.StateInProgress, s.Issues[i].Version+1
		s.event(actor, to, "issue.changed", []issues.Change{{Field: "state", From: string(from), To: string(issues.StateInProgress)}})
	}
	return nil
}

// Labels lists the organization's labels by name, with how many issues carry each.
func (s *Store) Labels(_ context.Context, orgID uuid.UUID) ([]issues.Label, error) {
	var out []issues.Label
	for _, l := range s.Kept {
		if l.OrgID == orgID {
			out = append(out, s.counted(l))
		}
	}
	slices.SortFunc(out, func(a, b issues.Label) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out, nil
}

// counted is l with how many issues carry it.
func (s *Store) counted(l issues.Label) issues.Label {
	l.Issues = 0
	for _, i := range s.Issues {
		if slices.ContainsFunc(i.Labels, func(c issues.IssueLabel) bool { return c.ID == l.ID }) {
			l.Issues++
		}
	}
	return l
}

// Label finds one of the organization's labels.
func (s *Store) Label(_ context.Context, orgID, labelID uuid.UUID) (issues.Label, error) {
	l, ok := s.Kept[labelID]
	if !ok || l.OrgID != orgID {
		return issues.Label{}, issues.ErrLabelNotFound
	}
	return s.counted(l), nil
}

// LabelsOf finds those of ids that are the organization's labels, by name.
func (s *Store) LabelsOf(_ context.Context, orgID uuid.UUID, ids []uuid.UUID) ([]issues.IssueLabel, error) {
	var out []issues.IssueLabel
	for _, id := range ids {
		if l, ok := s.Kept[id]; ok && l.OrgID == orgID {
			out = append(out, issues.IssueLabel{ID: l.ID, Name: l.Name, Color: l.Color})
		}
	}
	slices.SortFunc(out, func(a, b issues.IssueLabel) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out, nil
}

// taken reports whether another of the organization's labels than id is named name, whatever their case.
func (s *Store) taken(orgID, id uuid.UUID, name string) bool {
	for _, l := range s.Kept {
		if l.OrgID == orgID && l.ID != id && strings.EqualFold(l.Name, name) {
			return true
		}
	}
	return false
}

// CreateLabel records the label.
func (s *Store) CreateLabel(_ context.Context, _ uuid.UUID, label issues.Label) (issues.Label, error) {
	if s.Fail != nil {
		return issues.Label{}, s.Fail
	}
	if s.taken(label.OrgID, label.ID, label.Name) {
		return issues.Label{}, issues.ErrLabelTaken
	}
	s.Kept[label.ID] = label
	return label, nil
}

// UpdateLabel writes the label over the one at version, and counts a change on its issues when its name or colour
// changes.
func (s *Store) UpdateLabel(_ context.Context, _ uuid.UUID, label issues.Label, version int64) (issues.Label, error) {
	if s.Fail != nil {
		return issues.Label{}, s.Fail
	}
	cur, ok := s.Kept[label.ID]
	switch {
	case !ok:
		return issues.Label{}, issues.ErrLabelNotFound
	case cur.Version != version:
		return issues.Label{}, issues.ErrLabelChanged
	case s.taken(label.OrgID, label.ID, label.Name):
		return issues.Label{}, issues.ErrLabelTaken
	}
	label.Version = version + 1
	s.Kept[label.ID] = label
	if cur.Name != label.Name || cur.Color != label.Color {
		s.renew(label.ID)
	}
	return s.counted(label), nil
}

// DeleteLabel deletes the label at version, which takes it off its issues.
func (s *Store) DeleteLabel(_ context.Context, _ uuid.UUID, label issues.Label, version int64) error {
	if s.Fail != nil {
		return s.Fail
	}
	cur, ok := s.Kept[label.ID]
	switch {
	case !ok:
		return issues.ErrLabelNotFound
	case cur.Version != version:
		return issues.ErrLabelChanged
	}
	s.renew(label.ID)
	delete(s.Kept, label.ID)
	for i := range s.Issues {
		s.Issues[i].Labels = slices.DeleteFunc(s.Issues[i].Labels, func(l issues.IssueLabel) bool { return l.ID == label.ID })
	}
	return nil
}

// renew counts a change on the issues that carry the label.
func (s *Store) renew(labelID uuid.UUID) {
	for i := range s.Issues {
		if slices.ContainsFunc(s.Issues[i].Labels, func(l issues.IssueLabel) bool { return l.ID == labelID }) {
			s.Issues[i].Version++
		}
	}
}
