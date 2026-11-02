package issues_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/issues"
	"cobranch.dev/internal/services/orgs"
)

func ptr[T any](v T) *T { return &v }

// label creates a label of Acme's as Olivia, its Owner.
func (f *fixture) label(t *testing.T, name string, color issues.Color) issues.Label {
	t.Helper()
	l, err := f.service.CreateLabel(context.Background(), f.olivia, f.acme.ID, issues.LabelInput{Name: name, Color: color})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestCreateLabel(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.label(t, "Bug", issues.ColorRed)
	for _, tt := range []struct {
		name   string
		viewer func(f *fixture) issues.Viewer
		in     issues.LabelInput
		want   error
	}{
		{name: "an Owner's, gray by default", viewer: func(f *fixture) issues.Viewer { return f.olivia }, in: issues.LabelInput{Name: "  Docs ", Description: "Guides"}},
		{name: "a Member's", viewer: func(f *fixture) issues.Viewer { return f.max }, in: issues.LabelInput{Name: "UI"}, want: auth.ErrForbidden},
		{name: "a Guest's", viewer: func(f *fixture) issues.Viewer { return f.gwen }, in: issues.LabelInput{Name: "UI"}, want: auth.ErrForbidden},
		{name: "an outsider's", viewer: func(f *fixture) issues.Viewer { return f.nia }, in: issues.LabelInput{Name: "UI"}, want: orgs.ErrNotFound},
		{name: "a name another has, in another case", viewer: func(f *fixture) issues.Viewer { return f.olivia }, in: issues.LabelInput{Name: "bug"}, want: issues.ErrLabelTaken},
		{name: "no name", viewer: func(f *fixture) issues.Viewer { return f.olivia }, in: issues.LabelInput{Name: " "}, want: &orgs.ValidationError{}},
		{name: "a long name", viewer: func(f *fixture) issues.Viewer { return f.olivia }, in: issues.LabelInput{Name: string(make([]byte, 51))}, want: &orgs.ValidationError{}},
		{name: "another colour", viewer: func(f *fixture) issues.Viewer { return f.olivia }, in: issues.LabelInput{Name: "UI", Color: "pink"}, want: &orgs.ValidationError{}},
		{name: "a description on two lines", viewer: func(f *fixture) issues.Viewer { return f.olivia }, in: issues.LabelInput{Name: "UI", Description: "a\nb"}, want: &orgs.ValidationError{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.service.CreateLabel(ctx, tt.viewer(f), f.acme.ID, tt.in)
			if failed(t, "CreateLabel()", err, tt.want) {
				return
			}
			if got.Name != "Docs" || got.Color != issues.ColorGray || got.Description != "Guides" || got.Version != 1 || got.OrgID != f.acme.ID {
				t.Errorf("CreateLabel() = %+v", got)
			}
		})
	}
	labels, err := f.service.Labels(ctx, f.gwen, f.acme.ID)
	if !errors.Is(err, auth.ErrForbidden) {
		t.Errorf("a Guest's Labels() = %v, %v; want %v", labels, err, auth.ErrForbidden)
	}
	if labels, err = f.service.Labels(ctx, f.max, f.acme.ID); err != nil || len(labels) != 2 || labels[0].Name != "Bug" || labels[1].Name != "Docs" {
		t.Errorf("a Member's Labels() = %+v, %v; want Bug and Docs", labels, err)
	}
}

func TestIssueLabels(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	bug, ui := f.label(t, "Bug", issues.ColorRed), f.label(t, "ui", issues.ColorBlue)
	issue := f.create(t, f.max, issues.Input{Title: "Fix the footer", LabelIDs: []uuid.UUID{ui.ID, bug.ID, ui.ID}})
	if len(issue.Labels) != 2 || issue.Labels[0].Name != "Bug" || issue.Labels[1].Name != "ui" || issue.Labels[0].Color != issues.ColorRed {
		t.Fatalf("Create() labels = %+v, want Bug and ui", issue.Labels)
	}
	other := f.create(t, f.max, issues.Input{Title: "Docs"})
	for _, tt := range []struct {
		name   string
		labels []uuid.UUID
		want   error
	}{
		{name: "another organization's", labels: []uuid.UUID{uuid.New()}, want: &orgs.ValidationError{}},
		{name: "too many", labels: func() []uuid.UUID {
			var ids []uuid.UUID
			for range 21 {
				ids = append(ids, uuid.New())
			}
			return ids
		}(), want: &orgs.ValidationError{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.service.Update(ctx, f.max, f.acme.ID, other.Number, issues.Patch{LabelIDs: &tt.labels})
			failed(t, "Update()", err, tt.want)
		})
	}
	updated, err := f.service.Update(ctx, f.max, f.acme.ID, issue.Number, issues.Patch{LabelIDs: &[]uuid.UUID{ui.ID}, Version: issue.Version})
	if err != nil || len(updated.Labels) != 1 || updated.Labels[0].ID != ui.ID {
		t.Fatalf("Update() = %+v, %v; want ui alone", updated.Labels, err)
	}
	// The same labels again change nothing.
	if again, err := f.service.Update(ctx, f.max, f.acme.ID, issue.Number, issues.Patch{LabelIDs: &[]uuid.UUID{ui.ID}}); err != nil || again.Version != updated.Version {
		t.Errorf("Update() with the same labels = version %d, %v; want %d", again.Version, err, updated.Version)
	}
	events, err := f.service.Activity(ctx, f.max, f.acme.ID, issue.Number)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if len(last.Changes) != 1 || last.Changes[0] != (issues.Change{Field: "labels", From: "Bug, ui", To: "ui"}) {
		t.Errorf("the last event's changes = %+v, want labels from Bug, ui to ui", last.Changes)
	}
	page, err := f.service.List(ctx, f.max, f.acme.ID, issues.Filter{LabelIDs: []uuid.UUID{ui.ID, ui.ID}})
	if err != nil || len(page.Issues) != 1 || page.Issues[0].Number != issue.Number {
		t.Errorf("List(ui) = %+v, %v; want %s alone", page.Issues, err, issue.Identifier())
	}
	if page, err = f.service.List(ctx, f.max, f.acme.ID, issues.Filter{LabelIDs: []uuid.UUID{ui.ID, bug.ID}}); err != nil || len(page.Issues) != 0 {
		t.Errorf("List(ui, Bug) = %+v, %v; want none", page.Issues, err)
	}
}

func TestUpdateLabel(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	bug, ui := f.label(t, "Bug", issues.ColorRed), f.label(t, "UI", issues.ColorBlue)
	issue := f.create(t, f.max, issues.Input{Title: "Fix the footer", LabelIDs: []uuid.UUID{bug.ID}})
	for _, tt := range []struct {
		name   string
		viewer func(f *fixture) issues.Viewer
		id     uuid.UUID
		p      issues.LabelPatch
		want   error
	}{
		{name: "a Member's", viewer: func(f *fixture) issues.Viewer { return f.max }, id: bug.ID, p: issues.LabelPatch{Name: ptr("Defect")}, want: auth.ErrForbidden},
		{name: "a stale one", viewer: func(f *fixture) issues.Viewer { return f.olivia }, id: bug.ID, p: issues.LabelPatch{Name: ptr("Defect"), Version: 2}, want: issues.ErrLabelChanged},
		{name: "another's name", viewer: func(f *fixture) issues.Viewer { return f.olivia }, id: bug.ID, p: issues.LabelPatch{Name: ptr("ui")}, want: issues.ErrLabelTaken},
		{name: "another colour", viewer: func(f *fixture) issues.Viewer { return f.olivia }, id: bug.ID, p: issues.LabelPatch{Color: ptr(issues.Color("pink"))}, want: &orgs.ValidationError{}},
		{name: "a missing one", viewer: func(f *fixture) issues.Viewer { return f.olivia }, id: uuid.New(), p: issues.LabelPatch{Name: ptr("Defect")}, want: issues.ErrLabelNotFound},
		{name: "an Owner's", viewer: func(f *fixture) issues.Viewer { return f.olivia }, id: bug.ID, p: issues.LabelPatch{Name: ptr("Defect"), Color: ptr(issues.ColorYellow), Version: 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.service.UpdateLabel(ctx, tt.viewer(f), f.acme.ID, tt.id, tt.p)
			if failed(t, "UpdateLabel()", err, tt.want) {
				return
			}
			if got.Name != "Defect" || got.Color != issues.ColorYellow || got.Version != 2 || got.Issues != 1 {
				t.Errorf("UpdateLabel() = %+v", got)
			}
		})
	}
	// The issue shows the label as it now is, at another version, so a client that cached it reads it again.
	got, err := f.service.Get(ctx, f.max, f.acme.ID, issue.Number)
	if err != nil || len(got.Labels) != 1 || got.Labels[0].Name != "Defect" || got.Version == issue.Version {
		t.Errorf("Get() = %+v at version %d, %v; want Defect at a version after %d", got.Labels, got.Version, err, issue.Version)
	}
	// A change of its description alone leaves its issues as they are.
	if _, err := f.service.UpdateLabel(ctx, f.olivia, f.acme.ID, ui.ID, issues.LabelPatch{Description: ptr("How it looks")}); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteLabel(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	bug := f.label(t, "Bug", issues.ColorRed)
	issue := f.create(t, f.max, issues.Input{Title: "Fix the footer", LabelIDs: []uuid.UUID{bug.ID}})
	if err := f.service.DeleteLabel(ctx, f.max, f.acme.ID, bug.ID, 0); !errors.Is(err, auth.ErrForbidden) {
		t.Errorf("a Member's DeleteLabel() = %v, want %v", err, auth.ErrForbidden)
	}
	if err := f.service.DeleteLabel(ctx, f.olivia, f.acme.ID, bug.ID, 3); !errors.Is(err, issues.ErrLabelChanged) {
		t.Errorf("a stale DeleteLabel() = %v, want %v", err, issues.ErrLabelChanged)
	}
	if err := f.service.DeleteLabel(ctx, f.olivia, f.acme.ID, bug.ID, 1); err != nil {
		t.Fatal(err)
	}
	if got, err := f.service.Get(ctx, f.max, f.acme.ID, issue.Number); err != nil || len(got.Labels) != 0 || got.Version == issue.Version {
		t.Errorf("Get() = %+v at version %d, %v; want no labels, at a version after %d", got.Labels, got.Version, err, issue.Version)
	}
	if _, err := f.service.Label(ctx, f.max, f.acme.ID, bug.ID); !errors.Is(err, issues.ErrLabelNotFound) {
		t.Errorf("Label() after DeleteLabel() = %v, want %v", err, issues.ErrLabelNotFound)
	}
}
