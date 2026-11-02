package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/issues"
)

// IssueStore is issues.Store on PostgreSQL.
type IssueStore struct {
	pool *pgxpool.Pool
	q    *queries.Queries
}

// NewIssueStore returns issues' store on pool's database.
func NewIssueStore(pool *pgxpool.Pool) *IssueStore {
	return &IssueStore{pool: pool, q: queries.New(pool)}
}

var _ issues.Store = (*IssueStore)(nil)

// Create numbers the issue after its organization's last, records it and appends event issue.created, in one
// transaction that acts for actor (issues.Store).
func (s *IssueStore) Create(ctx context.Context, actor uuid.UUID, issue issues.Issue) (issues.Issue, error) {
	var out issues.Issue
	err := transact(ctx, s.pool, s.q, "creating the issue", actor, func(ctx context.Context, q *queries.Queries) error {
		number, err := q.NextIssueNumber(ctx, issue.OrgID)
		if err != nil {
			return fmt.Errorf("numbering the issue: %w", err)
		}
		if err := q.InsertIssue(ctx, queries.InsertIssueParams{ID: issue.ID, OrgID: issue.OrgID, Number: number, Title: issue.Title,
			Description: issue.Description, State: string(issue.State), Priority: int16(issue.Priority), AssigneeID: principalID(issue.Assignee),
			RepoID: repositoryID(issue.Repository), CreatedBy: principalID(issue.CreatedBy), CreatedAt: issue.CreatedAt, ClosedAt: issue.ClosedAt,
		}); err != nil {
			return fmt.Errorf("inserting the issue: %w", err)
		}
		if err := issueEvent(ctx, q, issue.OrgID, actor, "issue.created", issue.ID, map[string]any{"number": number, "title": issue.Title}); err != nil {
			return err
		}
		row, err := q.IssueByNumber(ctx, issue.OrgID, number)
		if err != nil {
			return fmt.Errorf("reading the issue back: %w", err)
		}
		out = issueOf(row)
		return nil
	})
	return out, err
}

// Get finds one of the organization's issues by number, or issues.ErrNotFound (issues.Store).
func (s *IssueStore) Get(ctx context.Context, orgID uuid.UUID, number int) (issues.Issue, error) {
	if number < 1 || number > maxInt32 {
		return issues.Issue{}, issues.ErrNotFound
	}
	row, err := s.q.IssueByNumber(ctx, orgID, int32(number))
	if errors.Is(err, pgx.ErrNoRows) {
		return issues.Issue{}, issues.ErrNotFound
	}
	if err != nil {
		return issues.Issue{}, fmt.Errorf("finding issue %d: %w", number, err)
	}
	return issueOf(row), nil
}

// maxInt32 is the largest number an issue takes.
const maxInt32 = 1<<31 - 1

// List lists a page of the organization's issues that q matches, newest first, and whether more follow
// (issues.Store).
func (s *IssueStore) List(ctx context.Context, orgID uuid.UUID, q issues.Query) ([]issues.Issue, bool, error) {
	states := make([]string, 0, len(q.States))
	for _, st := range q.States {
		states = append(states, string(st))
	}
	number := q.Number
	if number > maxInt32 {
		number = 0
	}
	rows, err := s.q.ListIssues(ctx, queries.ListIssuesParams{OrgID: orgID, States: states, AssigneeID: q.AssigneeID, RepoID: q.RepositoryID,
		Pattern: pattern(q.Text), Number: int32(number), Below: int32(q.Below), Lim: int32(q.Limit + 1)})
	if err != nil {
		return nil, false, fmt.Errorf("listing the issues: %w", err)
	}
	more := len(rows) > q.Limit
	if more {
		rows = rows[:q.Limit]
	}
	out := make([]issues.Issue, 0, len(rows))
	for _, r := range rows {
		out = append(out, issueOf(queries.IssueByNumberRow(r)))
	}
	return out, more, nil
}

// pattern is an ILIKE pattern that matches text anywhere, its own wildcards escaped; "" for no text.
func pattern(text string) string {
	if text = strings.TrimSpace(text); text == "" {
		return ""
	}
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(text) + "%"
}

// Update writes the issue's fields over the one at version and appends event issue.changed with its changes, in one
// transaction that acts for actor (issues.Store).
func (s *IssueStore) Update(ctx context.Context, actor uuid.UUID, issue issues.Issue, version int64, changes []issues.Change) (issues.Issue, error) {
	out := issue
	err := transact(ctx, s.pool, s.q, "changing the issue", actor, func(ctx context.Context, q *queries.Queries) error {
		row, err := q.UpdateIssue(ctx, queries.UpdateIssueParams{ID: issue.ID, Version: version, Title: issue.Title, Description: issue.Description,
			State: string(issue.State), Priority: int16(issue.Priority), AssigneeID: principalID(issue.Assignee), RepoID: repositoryID(issue.Repository),
			ClosedAt: issue.ClosedAt})
		if errors.Is(err, pgx.ErrNoRows) {
			return gone(ctx, q, issue)
		}
		if err != nil {
			return fmt.Errorf("writing the issue: %w", err)
		}
		out.Version, out.UpdatedAt = row.Version, row.UpdatedAt
		return issueEvent(ctx, q, issue.OrgID, actor, "issue.changed", issue.ID, map[string]any{"changes": changesOf(changes)})
	})
	if err != nil {
		return issues.Issue{}, err
	}
	return out, nil
}

// gone is why an issue's write found no row: issues.ErrNotFound when it was deleted, issues.ErrChanged when it is at
// another version.
func gone(ctx context.Context, q *queries.Queries, issue issues.Issue) error {
	if _, err := q.IssueByNumber(ctx, issue.OrgID, int32(issue.Number)); errors.Is(err, pgx.ErrNoRows) {
		return issues.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("finding issue %d: %w", issue.Number, err)
	}
	return issues.ErrChanged
}

// Delete deletes the issue at version and its activity, and appends event issue.deleted at its organization, in one
// transaction that acts for actor (issues.Store).
func (s *IssueStore) Delete(ctx context.Context, actor uuid.UUID, issue issues.Issue, version int64) error {
	return transact(ctx, s.pool, s.q, "deleting the issue", actor, func(ctx context.Context, q *queries.Queries) error {
		deleted, err := q.DeleteIssue(ctx, issue.ID, version)
		if err != nil {
			return fmt.Errorf("deleting the issue: %w", err)
		}
		if deleted == 0 {
			return gone(ctx, q, issue)
		}
		if err := q.DeleteIssueEvents(ctx, &issue.ID); err != nil {
			return fmt.Errorf("deleting the issue's activity: %w", err)
		}
		// Its subject is the organization's, not the issue's: the issue's activity went with it.
		return orgEvent(ctx, q, &issue.OrgID, actor, "issue.deleted", issue.OrgID,
			map[string]any{"issue": issue.ID, "number": issue.Number, "title": issue.Title})
	})
}

// Activity lists the issue's events, oldest first (issues.Store).
func (s *IssueStore) Activity(ctx context.Context, issueID uuid.UUID) ([]issues.Event, error) {
	rows, err := s.q.IssueEvents(ctx, &issueID)
	if err != nil {
		return nil, fmt.Errorf("listing the issue's events: %w", err)
	}
	out := make([]issues.Event, 0, len(rows))
	for _, r := range rows {
		e := issues.Event{ID: r.ID, Kind: r.Kind, At: r.At}
		if r.ActorName != nil {
			e.Actor = &issues.Principal{ID: r.ActorID, DisplayName: *r.ActorName}
		}
		var details struct {
			Changes []change `json:"changes"`
		}
		if err := json.Unmarshal(r.Details, &details); err != nil {
			return nil, fmt.Errorf("reading event %s: %w", r.ID, err)
		}
		for _, c := range details.Changes {
			e.Changes = append(e.Changes, issues.Change{Field: c.Field, From: c.From, To: c.To, FromID: c.FromID, ToID: c.ToID})
		}
		out = append(out, e)
	}
	return out, nil
}

// Member finds one of the organization's members, or issues.ErrNotFound (issues.Store).
func (s *IssueStore) Member(ctx context.Context, orgID, principalID uuid.UUID) (issues.Principal, error) {
	row, err := s.q.OrganizationMember(ctx, orgID, principalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return issues.Principal{}, issues.ErrNotFound
	}
	if err != nil {
		return issues.Principal{}, fmt.Errorf("finding the member: %w", err)
	}
	return issues.Principal{ID: row.ID, DisplayName: row.DisplayName}, nil
}

// Repository finds one of the organization's repositories, or issues.ErrNotFound (issues.Store).
func (s *IssueStore) Repository(ctx context.Context, orgID, repoID uuid.UUID) (issues.Repository, error) {
	row, err := s.q.OrganizationRepository(ctx, orgID, repoID)
	if errors.Is(err, pgx.ErrNoRows) {
		return issues.Repository{}, issues.ErrNotFound
	}
	if err != nil {
		return issues.Repository{}, fmt.Errorf("finding the repository: %w", err)
	}
	return issues.Repository{ID: row.ID, Slug: row.Slug, Name: row.Name}, nil
}

// change is an issues.Change as event issue.changed records it.
type change struct {
	Field  string    `json:"field"`
	From   string    `json:"from,omitempty"`
	To     string    `json:"to,omitempty"`
	FromID uuid.UUID `json:"fromId,omitzero"`
	ToID   uuid.UUID `json:"toId,omitzero"`
}

func changesOf(changes []issues.Change) []change {
	out := make([]change, 0, len(changes))
	for _, c := range changes {
		out = append(out, change{Field: c.Field, From: c.From, To: c.To, FromID: c.FromID, ToID: c.ToID})
	}
	return out
}

// issueEvent appends an event about the issue at its organization, as actor.
func issueEvent(ctx context.Context, q *queries.Queries, orgID, actor uuid.UUID, kind string, issueID uuid.UUID, details map[string]any) error {
	return orgEvent(ctx, q, &orgID, actor, kind, issueID, details)
}

// issueOf is the domain's view of an issues row, with its organization's slug and key and the names it refers to.
func issueOf(r queries.IssueByNumberRow) issues.Issue {
	i := r.Issue
	out := issues.Issue{ID: i.ID, OrgID: i.OrgID, OrgSlug: r.OrgSlug, Key: r.IssueKey, Number: int(i.Number), Title: i.Title,
		Description: i.Description, State: issues.State(i.State), Priority: issues.Priority(i.Priority), CreatedAt: i.CreatedAt,
		UpdatedAt: i.UpdatedAt, ClosedAt: i.ClosedAt, Version: i.Version}
	if i.AssigneeID != nil && r.AssigneeName != nil {
		out.Assignee = &issues.Principal{ID: *i.AssigneeID, DisplayName: *r.AssigneeName}
	}
	if i.CreatedBy != nil && r.CreatorName != nil {
		out.CreatedBy = &issues.Principal{ID: *i.CreatedBy, DisplayName: *r.CreatorName}
	}
	if i.RepoID != nil && r.RepoSlug != nil && r.RepoName != nil {
		out.Repository = &issues.Repository{ID: *i.RepoID, Slug: *r.RepoSlug, Name: *r.RepoName}
	}
	return out
}

func principalID(p *issues.Principal) *uuid.UUID {
	if p == nil {
		return nil
	}
	return &p.ID
}

func repositoryID(r *issues.Repository) *uuid.UUID {
	if r == nil {
		return nil
	}
	return &r.ID
}
