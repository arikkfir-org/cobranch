package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/issues"
)

// Labels lists the organization's labels by name, with how many issues carry each (issues.LabelStore).
func (s *IssueStore) Labels(ctx context.Context, orgID uuid.UUID) ([]issues.Label, error) {
	rows, err := s.q.OrganizationLabels(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("listing the labels: %w", err)
	}
	out := make([]issues.Label, 0, len(rows))
	for _, r := range rows {
		out = append(out, labelOf(r.Label, r.Issues))
	}
	return out, nil
}

// Label finds one of the organization's labels, or issues.ErrLabelNotFound (issues.LabelStore).
func (s *IssueStore) Label(ctx context.Context, orgID, labelID uuid.UUID) (issues.Label, error) {
	row, err := s.q.LabelByID(ctx, orgID, labelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return issues.Label{}, issues.ErrLabelNotFound
	}
	if err != nil {
		return issues.Label{}, fmt.Errorf("finding the label: %w", err)
	}
	return labelOf(row.Label, row.Issues), nil
}

// LabelsOf finds those of ids that are the organization's labels, by name (issues.LabelStore).
func (s *IssueStore) LabelsOf(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) ([]issues.IssueLabel, error) {
	rows, err := s.q.OrganizationLabelsOf(ctx, orgID, ids)
	if err != nil {
		return nil, fmt.Errorf("finding the labels: %w", err)
	}
	out := make([]issues.IssueLabel, 0, len(rows))
	for _, r := range rows {
		out = append(out, issues.IssueLabel{ID: r.ID, Name: r.Name, Color: issues.Color(r.Color)})
	}
	return out, nil
}

// CreateLabel records the label and appends event label.created at its organization, in one transaction that acts
// for actor (issues.LabelStore).
func (s *IssueStore) CreateLabel(ctx context.Context, actor uuid.UUID, label issues.Label) (issues.Label, error) {
	err := transact(ctx, s.pool, s.q, "creating the label", actor, func(ctx context.Context, q *queries.Queries) error {
		if err := q.InsertLabel(ctx, queries.InsertLabelParams{ID: label.ID, OrgID: label.OrgID, Name: label.Name, Color: string(label.Color),
			Description: label.Description, CreatedAt: label.CreatedAt}); err != nil {
			return fmt.Errorf("inserting the label: %w", err)
		}
		return labelEvent(ctx, q, actor, "label.created", label)
	})
	if err != nil {
		return issues.Label{}, taken(err)
	}
	return label, nil
}

// UpdateLabel writes the label's fields over the one at version, counts a change on the issues that carry it when its
// name or colour changes, and appends event label.changed, in one transaction that acts for actor
// (issues.LabelStore).
func (s *IssueStore) UpdateLabel(ctx context.Context, actor uuid.UUID, label issues.Label, version int64) (issues.Label, error) {
	out := label
	err := transact(ctx, s.pool, s.q, "changing the label", actor, func(ctx context.Context, q *queries.Queries) error {
		cur, err := q.LabelByID(ctx, label.OrgID, label.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return issues.ErrLabelNotFound
		}
		if err != nil {
			return fmt.Errorf("finding the label: %w", err)
		}
		row, err := q.UpdateLabel(ctx, queries.UpdateLabelParams{ID: label.ID, Version: version, Name: label.Name, Color: string(label.Color),
			Description: label.Description})
		if errors.Is(err, pgx.ErrNoRows) {
			return issues.ErrLabelChanged
		}
		if err != nil {
			return fmt.Errorf("writing the label: %w", err)
		}
		out.Version, out.UpdatedAt, out.Issues = row.Version, row.UpdatedAt, int(cur.Issues)
		if cur.Label.Name != label.Name || cur.Label.Color != string(label.Color) {
			if err := q.RenewLabelledIssues(ctx, label.ID); err != nil {
				return fmt.Errorf("renewing the label's issues: %w", err)
			}
		}
		return labelEvent(ctx, q, actor, "label.changed", label)
	})
	if err != nil {
		return issues.Label{}, taken(err)
	}
	return out, nil
}

// DeleteLabel deletes the label at version, which takes it off its issues after counting a change on them, and
// appends event label.deleted, in one transaction that acts for actor (issues.LabelStore).
func (s *IssueStore) DeleteLabel(ctx context.Context, actor uuid.UUID, label issues.Label, version int64) error {
	return transact(ctx, s.pool, s.q, "deleting the label", actor, func(ctx context.Context, q *queries.Queries) error {
		if err := q.RenewLabelledIssues(ctx, label.ID); err != nil {
			return fmt.Errorf("renewing the label's issues: %w", err)
		}
		deleted, err := q.DeleteLabel(ctx, label.ID, version)
		if err != nil {
			return fmt.Errorf("deleting the label: %w", err)
		}
		if deleted == 0 {
			if _, err := q.LabelByID(ctx, label.OrgID, label.ID); errors.Is(err, pgx.ErrNoRows) {
				return issues.ErrLabelNotFound
			} else if err != nil {
				return fmt.Errorf("finding the label: %w", err)
			}
			return issues.ErrLabelChanged
		}
		return labelEvent(ctx, q, actor, "label.deleted", label)
	})
}

// taken is issues.ErrLabelTaken for a name another label has, and err otherwise.
func taken(err error) error {
	var conflict *ConflictError
	if errors.As(err, &conflict) && conflict.Constraint == "labels_name" {
		return issues.ErrLabelTaken
	}
	return err
}

// labelEvent appends an event about the label at its organization, as actor.
func labelEvent(ctx context.Context, q *queries.Queries, actor uuid.UUID, kind string, label issues.Label) error {
	return orgEvent(ctx, q, &label.OrgID, actor, kind, label.ID, map[string]any{"name": label.Name, "color": label.Color})
}

// relabel makes the issue carry exactly the labels, in the transaction q runs in.
func relabel(ctx context.Context, q *queries.Queries, issueID uuid.UUID, labels []issues.IssueLabel) error {
	ids := make([]uuid.UUID, 0, len(labels))
	for _, l := range labels {
		ids = append(ids, l.ID)
	}
	if err := q.UnlabelIssue(ctx, issueID, ids); err != nil {
		return fmt.Errorf("taking labels off the issue: %w", err)
	}
	if err := q.LabelIssue(ctx, issueID, ids); err != nil {
		return fmt.Errorf("putting labels on the issue: %w", err)
	}
	return nil
}

// labelOf is the domain's view of a labels row, which count issues carry.
func labelOf(l queries.Label, count int64) issues.Label {
	return issues.Label{ID: l.ID, OrgID: l.OrgID, Name: l.Name, Color: issues.Color(l.Color), Description: l.Description, Issues: int(count),
		CreatedAt: l.CreatedAt, UpdatedAt: l.UpdatedAt, Version: l.Version}
}

// issueLabelsOf are the labels an issue's row carries, as its query aggregates them.
func issueLabelsOf(raw []byte) []issues.IssueLabel {
	var labels []struct {
		ID    uuid.UUID `json:"id"`
		Name  string    `json:"name"`
		Color string    `json:"color"`
	}
	// The query builds this JSON itself: it never fails to read, and an issue shows no labels if it somehow did.
	_ = json.Unmarshal(raw, &labels)
	out := make([]issues.IssueLabel, 0, len(labels))
	for _, l := range labels {
		out = append(out, issues.IssueLabel{ID: l.ID, Name: l.Name, Color: issues.Color(l.Color)})
	}
	return out
}
