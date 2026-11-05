package code_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/orgs/orgstest"
	"cobranch.dev/internal/services/repos"
)

// viewedFixture is the fixture with Max's marks on main: README.md at sha(1) and gone.txt, a file main deletes, both
// marked an hour before now; and a repository Anonymous reads too.
func viewedFixture() (*fixture, code.Viewer) {
	f := newFixture()
	f.store.Viewed[f.max.ID.String()+"/"+f.repo.String()+"/main"] = []code.ViewedFile{
		{Path: "README.md", Object: sha(1), ViewedAt: now.Add(-time.Hour)}, {Path: "gone.txt", ViewedAt: now.Add(-time.Hour)}}
	o := orgstest.NewStore()
	o.Repos[f.repo] = uuid.New()
	o.Grant(f.max.ID, auth.RoleReader, auth.ScopeRepository, f.repo)
	o.Grant(auth.AnonymousID, auth.RoleReader, auth.ScopeRepository, f.repo)
	f.service = code.NewService(f.store, f.objects, f.markdown, auth.NewAuthorizer(o))
	return f, code.Viewer{Principal: auth.Anonymous}
}

// marked names marks as path@object, by path.
func marked(marks []code.ViewedFile) []string {
	out := make([]string, 0, len(marks))
	for _, m := range marks {
		out = append(out, m.Path+"@"+m.Object)
	}
	return out
}

func TestViewed(t *testing.T) {
	tests := []struct {
		name    string
		viewer  func(*fixture, code.Viewer) code.Viewer
		branch  string
		fail    error
		want    []string
		wantErr error
	}{
		{name: "the viewer's marks, by path", branch: "main", want: []string{"README.md@" + sha(1), "gone.txt@"}},
		{name: "none on a branch the viewer marked nothing on", branch: "feature/x", want: []string{}},
		{name: "a branch that isn't there", branch: "gone", wantErr: repos.ErrNotFound},
		{name: "a viewer who doesn't read the repository", viewer: func(f *fixture, _ code.Viewer) code.Viewer { return f.nia }, branch: "main",
			wantErr: repos.ErrNotFound},
		{name: "Anonymous, who has no marks", viewer: func(_ *fixture, anon code.Viewer) code.Viewer { return anon }, branch: "main",
			wantErr: auth.ErrForbidden},
		{name: "the store's failure", branch: "main", fail: errors.New("down"), wantErr: errors.New("down")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, anon := viewedFixture()
			f.store.Fail = tt.fail
			v := f.max
			if tt.viewer != nil {
				v = tt.viewer(f, anon)
			}
			got, err := f.service.Viewed(t.Context(), v, f.repo, tt.branch)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			if err == nil && !slices.Equal(marked(got), tt.want) {
				t.Errorf("got %q, want %q", marked(got), tt.want)
			}
		})
	}
}

// updateViewedCase is a change of Max's marks on main, unless viewer or branch say otherwise, while they are those tag
// names (whichever are there without one), after full marks as many as main keeps; want and kept are what it leaves.
type updateViewedCase struct {
	name    string
	viewer  func(*fixture, code.Viewer) code.Viewer
	branch  string
	tag     func([]code.ViewedFile) string
	changes []code.ViewedChange
	full    bool
	want    []string
	kept    []string
	wantErr error
}

func TestUpdateViewed(t *testing.T) {
	runUpdateViewed(t, []updateViewedCase{
		{name: "marks a file at its object, and one the branch deletes", changes: []code.ViewedChange{{Path: "a/b.go", Object: sha(2)},
			{Path: "old.txt"}}, want: []string{"README.md@" + sha(1), "a/b.go@" + sha(2), "gone.txt@", "old.txt@"}, kept: []string{"README.md", "gone.txt"}},
		{name: "marking a file again at its object keeps when it was marked", changes: []code.ViewedChange{{Path: "README.md", Object: sha(1)}},
			want: []string{"README.md@" + sha(1), "gone.txt@"}, kept: []string{"README.md", "gone.txt"}},
		{name: "marking a file at another object moves its mark", changes: []code.ViewedChange{{Path: "README.md", Object: sha(3)}},
			want: []string{"README.md@" + sha(3), "gone.txt@"}, kept: []string{"gone.txt"}},
		{name: "takes a mark off, and leaves alone a file without one",
			changes: []code.ViewedChange{{Path: "README.md", Unmark: true}, {Path: "never.txt", Unmark: true}}, want: []string{"gone.txt@"},
			kept: []string{"gone.txt"}},
		{name: "while the marks are still those the tag names", tag: code.ViewedTag, changes: []code.ViewedChange{{Path: "gone.txt", Unmark: true}},
			want: []string{"README.md@" + sha(1)}, kept: []string{"README.md"}},
		{name: "no changes answers the marks", want: []string{"README.md@" + sha(1), "gone.txt@"}, kept: []string{"README.md", "gone.txt"}},
	})
}

func TestUpdateViewedRefused(t *testing.T) {
	many := make([]code.ViewedChange, code.MaxViewedChanges+1)
	for i := range many {
		many[i] = code.ViewedChange{Path: fmt.Sprintf("f%d.txt", i)}
	}
	runUpdateViewed(t, []updateViewedCase{
		{name: "a tag of other marks", tag: func([]code.ViewedFile) string { return code.ViewedTag(nil) },
			changes: []code.ViewedChange{{Path: "gone.txt", Unmark: true}}, wantErr: code.ErrViewedChanged},
		{name: "more changes than a diff lists files", changes: many, wantErr: invalid("files.too_many")},
		{name: "no path", changes: []code.ViewedChange{{Object: sha(2)}}, wantErr: invalid("path.invalid")},
		{name: "a path that climbs out", changes: []code.ViewedChange{{Path: "../x", Object: sha(2)}}, wantErr: invalid("path.invalid")},
		{name: "a path twice", changes: []code.ViewedChange{{Path: "x", Object: sha(2)}, {Path: "x", Unmark: true}}, wantErr: invalid("path.repeated")},
		{name: "an object that isn't one's full name", changes: []code.ViewedChange{{Path: "x", Object: "abc"}}, wantErr: invalid("object.invalid")},
		{name: "more marks than a branch keeps", full: true, changes: []code.ViewedChange{{Path: "one-more.txt"}}, wantErr: invalid("viewed.too_many")},
		{name: "a branch that isn't there", branch: "gone", changes: []code.ViewedChange{{Path: "x"}}, wantErr: repos.ErrNotFound},
		{name: "a viewer who doesn't read the repository", viewer: func(f *fixture, _ code.Viewer) code.Viewer { return f.nia },
			changes: []code.ViewedChange{{Path: "x"}}, wantErr: repos.ErrNotFound},
		{name: "Anonymous, who keeps no marks", viewer: func(_ *fixture, anon code.Viewer) code.Viewer { return anon },
			changes: []code.ViewedChange{{Path: "x"}}, wantErr: auth.ErrForbidden},
	})
}

// runUpdateViewed runs each case on a fixture of its own, and checks that a refused change leaves the marks as they were.
func runUpdateViewed(t *testing.T, tests []updateViewedCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, anon := viewedFixture()
			key := f.max.ID.String() + "/" + f.repo.String() + "/main"
			if tt.full {
				fillViewed(f, key)
			}
			before := slices.Clone(f.store.Viewed[key])
			v, branch, tag := viewedRequest(f, anon, before, tt.viewer, tt.branch, tt.tag)
			got, err := f.service.UpdateViewed(t.Context(), v, f.repo, branch, tag, tt.changes)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				if !slices.Equal(f.store.Viewed[key], before) {
					t.Errorf("the marks changed to %q", marked(f.store.Viewed[key]))
				}
				return
			}
			if !slices.Equal(marked(got), tt.want) {
				t.Errorf("got %q, want %q", marked(got), tt.want)
			}
			checkMoments(t, got, before, tt.kept)
		})
	}
}

// fillViewed marks files on main for Max until the branch keeps as many of Max's marks as it may.
func fillViewed(f *fixture, key string) {
	for i := len(f.store.Viewed[key]); i < code.MaxViewedFiles; i++ {
		f.store.Viewed[key] = append(f.store.Viewed[key], code.ViewedFile{Path: fmt.Sprintf("z/%05d", i), ViewedAt: now})
	}
}

// viewedRequest is who changes marks, of which branch, while they are those which tag: Max, on main, whichever marks
// are there, unless viewer, branch or tag say otherwise; tag names the marks before.
func viewedRequest(f *fixture, anon code.Viewer, before []code.ViewedFile, viewer func(*fixture, code.Viewer) code.Viewer, branch string,
	tag func([]code.ViewedFile) string) (code.Viewer, string, string) {
	v, b, named := f.max, "main", code.AnyViewed
	if viewer != nil {
		v = viewer(f, anon)
	}
	if branch != "" {
		b = branch
	}
	if tag != nil {
		named = tag(before)
	}
	return v, b, named
}

// checkMoments checks that each of marks in kept keeps its moment from before, and that every other one is new.
func checkMoments(t *testing.T, marks, before []code.ViewedFile, kept []string) {
	t.Helper()
	for _, m := range marks {
		i := slices.IndexFunc(before, func(b code.ViewedFile) bool { return b.Path == m.Path })
		switch {
		case slices.Contains(kept, m.Path) && (i < 0 || !m.ViewedAt.Equal(before[i].ViewedAt)):
			t.Errorf("%s marked at %v, want it kept", m.Path, m.ViewedAt)
		case !slices.Contains(kept, m.Path) && i >= 0 && m.ViewedAt.Equal(before[i].ViewedAt):
			t.Errorf("%s kept its moment, want it marked now", m.Path)
		}
	}
}

func TestViewedTag(t *testing.T) {
	marks := []code.ViewedFile{{Path: "a.go", Object: sha(1), ViewedAt: now}, {Path: "b.go", ViewedAt: now}}
	tests := []struct {
		name  string
		other []code.ViewedFile
		same  bool
	}{
		{name: "the same marks", other: slices.Clone(marks), same: true},
		{name: "a mark taken off", other: marks[:1]},
		{name: "a mark at another object", other: []code.ViewedFile{{Path: "a.go", Object: sha(2), ViewedAt: now}, marks[1]}},
		{name: "a mark made again", other: []code.ViewedFile{{Path: "a.go", Object: sha(1), ViewedAt: now.Add(time.Microsecond)}, marks[1]}},
		{name: "a path and its neighbour's object run together", other: []code.ViewedFile{{Path: "a.go" + sha(1), ViewedAt: now}, marks[1]}},
		{name: "none", other: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := code.ViewedTag(tt.other) == code.ViewedTag(marks); got != tt.same {
				t.Errorf("tags equal: %v, want %v", got, tt.same)
			}
		})
	}
	if tag := code.ViewedTag(nil); tag != code.ViewedTag([]code.ViewedFile{}) || len(tag) != 24 {
		t.Errorf("no marks tagged %q, want one 24-character tag for none", tag)
	}
}
