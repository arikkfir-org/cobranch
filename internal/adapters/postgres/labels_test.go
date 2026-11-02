package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/issues"
)

// newLabel is a label of Acme's.
func (f *issueFixture) newLabel(t *testing.T, name string, color issues.Color) issues.Label {
	t.Helper()
	l, err := f.is.CreateLabel(context.Background(), f.olivia, issues.Label{ID: uuid.Must(uuid.NewV7()), OrgID: f.acme.ID, Name: name, Color: color,
		CreatedAt: now, UpdatedAt: now, Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestLabelStore(t *testing.T) {
	t.Parallel()
	f := newIssueFixture(t)
	ctx := context.Background()
	bug, ui := f.newLabel(t, "Bug", issues.ColorRed), f.newLabel(t, "ui", issues.ColorBlue)
	if _, err := f.is.CreateLabel(ctx, f.olivia, issues.Label{ID: uuid.New(), OrgID: f.acme.ID, Name: "BUG", Color: issues.ColorGray}); !errors.Is(err, issues.ErrLabelTaken) {
		t.Errorf("CreateLabel(BUG) = %v, want %v", err, issues.ErrLabelTaken)
	}
	issue := f.newIssue("Fix the footer")
	issue.Labels = []issues.IssueLabel{{ID: ui.ID}, {ID: bug.ID}}
	created := f.create(t, issue)
	if len(created.Labels) != 2 || created.Labels[0].Name != "Bug" || created.Labels[0].Color != issues.ColorRed || created.Labels[1].Name != "ui" {
		t.Fatalf("Create() labels = %+v, want Bug and ui", created.Labels)
	}
	f.create(t, f.newIssue("Write docs"))
	labels, err := f.is.Labels(ctx, f.acme.ID)
	if err != nil || len(labels) != 2 || labels[0].ID != bug.ID || labels[0].Issues != 1 || labels[1].ID != ui.ID {
		t.Fatalf("Labels() = %+v, %v; want Bug and ui, on one issue each", labels, err)
	}
	of, err := f.is.LabelsOf(ctx, f.acme.ID, []uuid.UUID{ui.ID, uuid.New()})
	if err != nil || len(of) != 1 || of[0].ID != ui.ID {
		t.Errorf("LabelsOf() = %+v, %v; want ui alone", of, err)
	}
	// Taking Bug off keeps ui, and lists only the issue that carries it.
	next := created
	next.Labels = []issues.IssueLabel{{ID: ui.ID, Name: "ui", Color: issues.ColorBlue}}
	if _, err := f.is.Update(ctx, f.max, next, created.Version, []issues.Change{{Field: "labels", From: "Bug, ui", To: "ui"}}); err != nil {
		t.Fatal(err)
	}
	list := func(ids ...uuid.UUID) []issues.Issue {
		t.Helper()
		page, _, err := f.is.List(ctx, f.acme.ID, issues.Query{States: issues.States, LabelIDs: ids, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	if got := list(ui.ID); len(got) != 1 || got[0].Number != created.Number || len(got[0].Labels) != 1 {
		t.Errorf("List(ui) = %+v, want %s carrying ui", got, created.Identifier())
	}
	if got := list(bug.ID); len(got) != 0 {
		t.Errorf("List(Bug) = %+v, want none", got)
	}
	if got := list(); len(got) != 2 {
		t.Errorf("List() = %d issues, want 2", len(got))
	}
	if n := count(t, f.pool, "SELECT count(*) FROM audit_log WHERE table_name = 'issue_labels' AND principal_id = $1", f.max); n != 1 {
		t.Errorf("%d audit rows of issue_labels by Max, want 1", n)
	}
}

func TestLabelStoreChanges(t *testing.T) {
	t.Parallel()
	f := newIssueFixture(t)
	ctx := context.Background()
	bug, ui := f.newLabel(t, "Bug", issues.ColorRed), f.newLabel(t, "ui", issues.ColorBlue)
	issue := f.newIssue("Fix the footer")
	issue.Labels = []issues.IssueLabel{{ID: bug.ID}}
	created := f.create(t, issue)
	version := func() int64 {
		t.Helper()
		got, err := f.is.Get(ctx, f.acme.ID, created.Number)
		if err != nil {
			t.Fatal(err)
		}
		return got.Version
	}
	// A description's change leaves the issue as it is; a name's counts a change on it.
	bug.Description = "Something works wrongly"
	if bug, err := f.is.UpdateLabel(ctx, f.olivia, bug, 1); err != nil || bug.Version != 2 || version() != created.Version {
		t.Fatalf("UpdateLabel(description) = %+v, %v, the issue at %d; want version 2, the issue at %d", bug, err, version(), created.Version)
	}
	bug.Name = "Defect"
	if _, err := f.is.UpdateLabel(ctx, f.olivia, bug, 1); !errors.Is(err, issues.ErrLabelChanged) {
		t.Errorf("a stale UpdateLabel() = %v, want %v", err, issues.ErrLabelChanged)
	}
	renamed, err := f.is.UpdateLabel(ctx, f.olivia, bug, 2)
	if err != nil || renamed.Version != 3 || version() != created.Version+1 {
		t.Fatalf("UpdateLabel(name) = %+v, %v, the issue at %d; want version 3, the issue at %d", renamed, err, version(), created.Version+1)
	}
	ui.Name = "DEFECT"
	if _, err := f.is.UpdateLabel(ctx, f.olivia, ui, 1); !errors.Is(err, issues.ErrLabelTaken) {
		t.Errorf("UpdateLabel(DEFECT) = %v, want %v", err, issues.ErrLabelTaken)
	}
	if err := f.is.DeleteLabel(ctx, f.olivia, renamed, 2); !errors.Is(err, issues.ErrLabelChanged) {
		t.Errorf("a stale DeleteLabel() = %v, want %v", err, issues.ErrLabelChanged)
	}
	if err := f.is.DeleteLabel(ctx, f.olivia, renamed, 3); err != nil {
		t.Fatal(err)
	}
	got, err := f.is.Get(ctx, f.acme.ID, created.Number)
	if err != nil || len(got.Labels) != 0 || got.Version != created.Version+2 {
		t.Errorf("Get() after DeleteLabel() = %+v at %d, %v; want no labels at %d", got.Labels, got.Version, err, created.Version+2)
	}
	if err := f.is.DeleteLabel(ctx, f.olivia, renamed, 3); !errors.Is(err, issues.ErrLabelNotFound) {
		t.Errorf("DeleteLabel() again = %v, want %v", err, issues.ErrLabelNotFound)
	}
	if _, err := f.is.Label(ctx, f.acme.ID, renamed.ID); !errors.Is(err, issues.ErrLabelNotFound) {
		t.Errorf("Label() after DeleteLabel() = %v, want %v", err, issues.ErrLabelNotFound)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM events WHERE kind LIKE 'label.%' AND org_id = $1 AND actor_id = $2", f.acme.ID, f.olivia); n != 5 {
		t.Errorf("%d label events, want 5: two created, two changed, one deleted", n)
	}
}
