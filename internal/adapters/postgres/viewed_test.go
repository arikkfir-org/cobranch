package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// viewedAt names marks as path@object's first letter, by path.
func viewedAt(marks []code.ViewedFile) []string {
	out := make([]string, 0, len(marks))
	for _, m := range marks {
		o := m.Object
		if o != "" {
			o = o[:1]
		}
		out = append(out, m.Path+"@"+o)
	}
	return out
}

// pass is a check that passes on any marks.
func pass([]code.ViewedFile) error { return nil }

func TestCodeStoreUpdateViewed(t *testing.T) {
	t.Parallel()
	f := newCodeFixture(t)
	ctx := context.Background()
	refused := errors.New("refused")
	tests := []struct {
		name    string
		actor   uuid.UUID
		branch  string
		check   func([]code.ViewedFile) error
		changes []code.ViewedChange
		at      time.Time
		want    []string
		wantErr error
	}{
		{name: "marks files, one the branch deletes", actor: f.olivia, branch: "feature", at: now,
			changes: []code.ViewedChange{{Path: "b.go", Object: shaB}, {Path: "a.go", Object: shaA}, {Path: "gone.go"}},
			want:    []string{"a.go@a", "b.go@b", "gone.go@"}},
		{name: "marking a file again at its object keeps it, at another moves it", actor: f.olivia, branch: "feature", at: now.Add(time.Hour),
			changes: []code.ViewedChange{{Path: "a.go", Object: shaA}, {Path: "b.go", Object: shaC}},
			want:    []string{"a.go@a", "b.go@c", "gone.go@"}},
		{name: "takes marks off, and leaves alone a file without one", actor: f.olivia, branch: "feature", at: now.Add(2 * time.Hour),
			changes: []code.ViewedChange{{Path: "gone.go", Unmark: true}, {Path: "never.go", Unmark: true}},
			want:    []string{"a.go@a", "b.go@c"}},
		{name: "a check that fails changes nothing", actor: f.olivia, branch: "feature", at: now.Add(3 * time.Hour),
			check:   func([]code.ViewedFile) error { return refused },
			changes: []code.ViewedChange{{Path: "a.go", Unmark: true}}, wantErr: refused},
		{name: "another person's marks are their own", actor: f.max, branch: "feature", at: now,
			changes: []code.ViewedChange{{Path: "c.go", Object: shaC}}, want: []string{"c.go@c"}},
		{name: "a branch's marks are its own", actor: f.olivia, branch: "main", at: now,
			changes: []code.ViewedChange{{Path: "d.go", Object: f.shaD}}, want: []string{"d.go@d"}},
		{name: "a branch a push deleted", actor: f.olivia, branch: "old", at: now, changes: []code.ViewedChange{{Path: "a.go"}},
			wantErr: repos.ErrNotFound},
		{name: "a name no branch has", actor: f.olivia, branch: "nothing", at: now, changes: []code.ViewedChange{{Path: "a.go"}},
			wantErr: repos.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := tt.check
			if check == nil {
				check = pass
			}
			before, _ := f.c.ViewedFiles(ctx, tt.actor, f.website.ID, tt.branch)
			got, err := f.c.UpdateViewed(ctx, tt.actor, f.website.ID, tt.branch, check, tt.changes, tt.at)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			read, rerr := f.c.ViewedFiles(ctx, tt.actor, f.website.ID, tt.branch)
			if tt.wantErr != nil {
				if !slices.Equal(viewedAt(read), viewedAt(before)) {
					t.Errorf("the marks changed from %q to %q", viewedAt(before), viewedAt(read))
				}
				return
			}
			if rerr != nil || !slices.Equal(viewedAt(got), tt.want) || !slices.EqualFunc(got, read, sameMark) {
				t.Errorf("got %q, read %q (%v), want %q", viewedAt(got), viewedAt(read), rerr, tt.want)
			}
			checkMarked(t, got, before, tt.changes, tt.at)
		})
	}
}

// sameMark compares two marks, their moments as instants.
func sameMark(a, b code.ViewedFile) bool {
	return a.Path == b.Path && a.Object == b.Object && a.ViewedAt.Equal(b.ViewedAt)
}

// checkMarked checks that each of marks a change set or moved is at at, and every other keeps its moment from before.
func checkMarked(t *testing.T, marks, before []code.ViewedFile, changes []code.ViewedChange, at time.Time) {
	t.Helper()
	for _, m := range marks {
		i := slices.IndexFunc(before, func(b code.ViewedFile) bool { return b.Path == m.Path })
		want := at
		if !slices.ContainsFunc(changes, func(c code.ViewedChange) bool { return c.Path == m.Path }) || (i >= 0 && before[i].Object == m.Object) {
			want = before[i].ViewedAt
		}
		if !m.ViewedAt.Equal(want) {
			t.Errorf("%s marked at %v, want %v", m.Path, m.ViewedAt, want)
		}
	}
}

func TestCodeStoreViewedLimit(t *testing.T) {
	t.Parallel()
	f := newCodeFixture(t)
	ctx := context.Background()
	id := f.liveBranchID(t, "feature")
	if _, err := f.pool.Exec(ctx, "INSERT INTO viewed_files (org_id, principal_id, branch_id, path, object, viewed_at) "+
		"SELECT $1, $2, $3, 'f/' || i, '', $4 FROM generate_series(2, $5) i", f.acme.ID, f.olivia, id, now, code.MaxViewedFiles); err != nil {
		t.Fatal(err)
	}
	if _, err := f.c.UpdateViewed(ctx, f.olivia, f.website.ID, "feature", pass, []code.ViewedChange{{Path: "f/1"}}, now); err != nil {
		t.Fatalf("the last mark a branch keeps: %v", err)
	}
	_, err := f.c.UpdateViewed(ctx, f.olivia, f.website.ID, "feature", pass, []code.ViewedChange{{Path: "f/0"}}, now)
	var verr *orgs.ValidationError
	if !errors.As(err, &verr) || verr.Code != "viewed.too_many" {
		t.Fatalf("one mark more: got %v, want viewed.too_many", err)
	}
	if n := count(t, f.pool, "SELECT count(*) FROM viewed_files WHERE branch_id = $1", id); n != code.MaxViewedFiles {
		t.Errorf("kept %d marks, want %d", n, code.MaxViewedFiles)
	}
}

func TestCodeStoreViewedBranchLivesAgain(t *testing.T) {
	t.Parallel()
	f := newCodeFixture(t)
	ctx := context.Background()
	if _, err := f.c.UpdateViewed(ctx, f.olivia, f.website.ID, "feature", pass, []code.ViewedChange{{Path: "a.go", Object: shaA}}, now); err != nil {
		t.Fatal(err)
	}
	for _, u := range []git.RefUpdate{{Name: "refs/heads/feature", Old: shaC, New: git.ZeroSHA}, {Name: "refs/heads/feature", Old: git.ZeroSHA, New: shaC}} {
		if err := f.g.Record(ctx, pushOf(f.website.ID, f.olivia, "", []git.RefUpdate{u})); err != nil {
			t.Fatal(err)
		}
	}
	got, err := f.c.ViewedFiles(ctx, f.olivia, f.website.ID, "feature")
	if err != nil || len(got) != 0 {
		t.Errorf("got %q, %v, want no marks on the branch that took the name", viewedAt(got), err)
	}
}

func TestCodeStoreViewedAudit(t *testing.T) {
	t.Parallel()
	f := newCodeFixture(t)
	ctx := context.Background()
	for _, c := range []code.ViewedChange{{Path: "a.go", Object: shaA}, {Path: "a.go", Object: shaB}, {Path: "a.go", Unmark: true}} {
		if _, err := f.c.UpdateViewed(ctx, f.max, f.website.ID, "feature", pass, []code.ViewedChange{c}, now); err != nil {
			t.Fatal(err)
		}
	}
	log := auditLog(t, f.pool, "viewed_files")
	var actions []string
	for _, r := range log {
		actions = append(actions, r.Action)
		if r.Principal == nil || *r.Principal != f.max {
			t.Errorf("a row names %v, want Max", r.Principal)
		}
	}
	if !slices.Equal(actions, []string{"insert", "update", "delete"}) {
		t.Errorf("got %q, want an insert, an update and a delete", actions)
	}
}

// liveBranchID is the ID of website's live branch of name.
func (f *codeFixture) liveBranchID(t *testing.T, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(context.Background(), "SELECT id FROM branches WHERE repo_id = $1 AND name = $2 AND deleted_at IS NULL",
		f.website.ID, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
