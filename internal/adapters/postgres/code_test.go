package postgres

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/repos"
)

// codeFixture is gitFixture after three pushes into website: main at d, which merged feature's c into b (a ← b ← d,
// with d's second parent c on a), feature at c, stray at e, an object no push recorded as a commit, and old, which
// lived at a until the third push deleted it. The push worker processed the first two.
type codeFixture struct {
	*gitFixture
	c             *CodeStore
	first, second git.Push
	shaD, shaE    string
}

func newCodeFixture(t *testing.T) *codeFixture {
	t.Helper()
	f := &codeFixture{gitFixture: newGitFixture(t), shaD: strings.Repeat("d", 40), shaE: strings.Repeat("e", 40)}
	f.c = NewCodeStore(f.pool)
	ctx := context.Background()
	ada := person("Ada Author", "ada@example.com")
	f.first = pushOf(f.website.ID, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA},
		{Name: "refs/heads/old", Old: git.ZeroSHA, New: shaA}}, commit(shaA, ada))
	f.second = pushOf(f.website.ID, f.olivia, "pack-2", []git.RefUpdate{{Name: "refs/heads/main", Old: shaA, New: f.shaD},
		{Name: "refs/heads/feature", Old: git.ZeroSHA, New: shaC}, {Name: "refs/heads/stray", Old: git.ZeroSHA, New: f.shaE}},
		commit(shaB, ada, shaA), commit(shaC, ada, shaA), commit(f.shaD, ada, shaB, shaC))
	for _, p := range []git.Push{f.first, f.second} {
		if err := f.g.Record(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	f.process(t)
	gone := pushOf(f.website.ID, f.olivia, "", []git.RefUpdate{{Name: "refs/heads/old", Old: shaA, New: git.ZeroSHA}})
	if err := f.g.Record(ctx, gone); err != nil {
		t.Fatal(err)
	}
	return f
}

func names(commits []code.Commit) []string {
	var out []string
	for _, c := range commits {
		out = append(out, c.SHA[:1])
	}
	return out
}

func TestCodeStoreBranches(t *testing.T) {
	t.Parallel()
	f := newCodeFixture(t)
	ctx := context.Background()
	got, err := f.c.Branches(ctx, f.website.ID)
	if err != nil {
		t.Fatal(err)
	}
	var live []string
	for _, b := range got {
		live = append(live, b.Name+"@"+b.Head[:1])
	}
	if want := []string{"feature@c", "main@d", "stray@e"}; !slices.Equal(live, want) {
		t.Errorf("Branches() = %v, want %v", live, want)
	}
	if got[1].Commit == nil || got[1].Commit.SHA != f.shaD || got[1].Commit.Author.Principal == nil || got[2].Commit != nil {
		t.Errorf("Branches() heads = %+v and %+v, want d with its author's principal, then none", got[1].Commit, got[2].Commit)
	}
	if other, err := f.c.Branches(ctx, f.secret.ID); err != nil || len(other) != 0 {
		t.Errorf("another repository's Branches() = %v, %v", other, err)
	}
	main, err := f.c.Branch(ctx, f.website.ID, "main")
	if err != nil || main.Head != f.shaD || main.Commit == nil || !slices.Equal(main.Commit.Parents, []string{shaB, shaC}) {
		t.Errorf("Branch(main) = %+v, %v", main, err)
	}
	for _, name := range []string{"old", "nothing"} {
		if _, err := f.c.Branch(ctx, f.website.ID, name); !errors.Is(err, repos.ErrNotFound) {
			t.Errorf("Branch(%s) = %v, want %v", name, err, repos.ErrNotFound)
		}
	}
}

// TestCodeStoreDeletedBranch runs its cases in order over one codeFixture, each pushing updates first: the latest
// deletion of a name answers, and none does while a branch of that name is live.
func TestCodeStoreDeletedBranch(t *testing.T) {
	t.Parallel()
	f := newCodeFixture(t)
	ctx := context.Background()
	tests := []struct {
		name     string
		updates  []git.RefUpdate
		branch   string
		wantHead string
	}{
		{name: "a branch a push deleted", branch: "old", wantHead: shaA},
		{name: "a live branch", branch: "feature"},
		{name: "a name no branch had", branch: "nothing"},
		{name: "a deleted branch's name, live again", updates: []git.RefUpdate{{Name: "refs/heads/old", Old: git.ZeroSHA, New: f.shaD}}, branch: "old"},
		{name: "the same name deleted again", updates: []git.RefUpdate{{Name: "refs/heads/old", Old: f.shaD, New: git.ZeroSHA}}, branch: "old",
			wantHead: f.shaD},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.updates != nil {
				if err := f.g.Record(ctx, pushOf(f.website.ID, f.olivia, "", tt.updates)); err != nil {
					t.Fatal(err)
				}
			}
			got, err := f.c.DeletedBranch(ctx, f.website.ID, tt.branch)
			if tt.wantHead == "" {
				if !errors.Is(err, repos.ErrNotFound) {
					t.Errorf("DeletedBranch(%s) = %+v, %v, want %v", tt.branch, got, err, repos.ErrNotFound)
				}
				return
			}
			if err != nil || got.Head != tt.wantHead || got.Commit == nil || got.Commit.SHA != tt.wantHead || got.DeletedAt.IsZero() ||
				got.DeletedBy == nil || got.DeletedBy.ID != f.olivia || got.Merge != nil {
				t.Errorf("DeletedBranch(%s) = %+v, %v, want it at %s, deleted by Olivia, never merged", tt.branch, got, err, tt.wantHead[:1])
			}
		})
	}
}

func TestCodeStoreFirstParents(t *testing.T) {
	t.Parallel()
	f := newCodeFixture(t)
	tests := []struct {
		name  string
		from  string
		limit int
		want  []string
	}{
		{name: "along first parents only", from: f.shaD, limit: 10, want: []string{"d", "b", "a"}},
		{name: "at most limit", from: f.shaD, limit: 2, want: []string{"d", "b"}},
		{name: "from a commit of a merged branch", from: shaC, limit: 10, want: []string{"c", "a"}},
		{name: "from an object that is no commit", from: f.shaE, limit: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.c.FirstParents(context.Background(), f.website.ID, tt.from, tt.limit)
			if err != nil || !slices.Equal(names(got), tt.want) {
				t.Errorf("FirstParents() = %v, %v, want %v", names(got), err, tt.want)
			}
		})
	}
	if got, err := f.c.FirstParents(context.Background(), f.secret.ID, f.shaD, 10); err != nil || len(got) != 0 {
		t.Errorf("another repository's FirstParents() = %v, %v", names(got), err)
	}
}

func TestCodeStoreCommit(t *testing.T) {
	t.Parallel()
	f := newCodeFixture(t)
	ctx := context.Background()
	c, p, err := f.c.Commit(ctx, f.website.ID, f.shaD)
	if err != nil {
		t.Fatal(err)
	}
	if c.FirstPush != f.second.ID || p.ID != f.second.ID || p.Transport != git.TransportHTTPS || p.Pusher == nil || p.Pusher.ID != f.olivia ||
		p.Pusher.DisplayName == "" || !p.At.After(now.AddDate(-1, 0, 0)) {
		t.Errorf("Commit(d) was pushed in %+v, want the second push, by Olivia", p)
	}
	if c.Author.Name != "Ada Author" || c.Author.Email != "ada@example.com" || c.Author.Zone != "+0200" || c.Author.Principal == nil ||
		c.Author.Principal.DisplayName != "Ada Author" || c.Committer.Principal == nil || c.Committer.Principal.ID != f.olivia {
		t.Errorf("Commit(d) by %+v, committed by %+v, want Ada's login-less user and Olivia", c.Author, c.Committer)
	}
	if c.Message != "Change d\n" || c.Tree != shaC || !slices.Equal(c.Parents, []string{shaB, shaC}) {
		t.Errorf("Commit(d) = %+v", c)
	}
	for _, missing := range []struct {
		repo repos.Repository
		sha  string
	}{{f.website, f.shaE}, {f.secret, f.shaD}} {
		if _, _, err := f.c.Commit(ctx, missing.repo.ID, missing.sha); !errors.Is(err, repos.ErrNotFound) {
			t.Errorf("Commit(%s) in %s = %v, want %v", missing.sha[:1], missing.repo.Slug, err, repos.ErrNotFound)
		}
	}
}

func TestCodeStoreTags(t *testing.T) {
	t.Parallel()
	f := newCodeFixture(t)
	ctx := context.Background()
	for _, tag := range []struct{ name, sha string }{{"v2", shaB}, {"V3", f.shaD}, {"v1.0", shaA}} {
		if _, err := f.pool.Exec(ctx, "INSERT INTO refs (repo_id, name, org_id, sha) VALUES ($1, $2, $3, $4)",
			f.website.ID, "refs/tags/"+tag.name, f.website.OrgID, tag.sha); err != nil {
			t.Fatal(err)
		}
	}
	got, err := f.c.Tags(ctx, f.website.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []code.Tag{{Name: "V3", Target: f.shaD}, {Name: "v1.0", Target: shaA}, {Name: "v2", Target: shaB}}
	if !slices.Equal(got, want) {
		t.Errorf("Tags() = %v, want %v, by name byte by byte, without the branches", got, want)
	}
	if other, err := f.c.Tags(ctx, f.secret.ID); err != nil || len(other) != 0 {
		t.Errorf("another repository's Tags() = %v, %v", other, err)
	}
}

func TestCodeStoreCommitsAt(t *testing.T) {
	t.Parallel()
	f := newCodeFixture(t)
	ctx := context.Background()
	got, err := f.c.CommitsAt(ctx, f.website.ID, []string{f.shaD, f.shaE, shaA})
	if err != nil || len(got) != 2 || got[f.shaD].Message != "Change d\n" || got[shaA].SHA != shaA || got[f.shaD].Author.Principal == nil {
		t.Errorf("CommitsAt(d, e, a) = %v, %v, want d with its author's principal and a, without e, which is no commit", got, err)
	}
	if other, err := f.c.CommitsAt(ctx, f.secret.ID, []string{f.shaD}); err != nil || len(other) != 0 {
		t.Errorf("another repository's CommitsAt() = %v, %v", other, err)
	}
	if none, err := f.c.CommitsAt(ctx, f.website.ID, nil); err != nil || len(none) != 0 {
		t.Errorf("CommitsAt() of nothing = %v, %v", none, err)
	}
}

func TestCodeStoreSlugs(t *testing.T) {
	t.Parallel()
	f := newCodeFixture(t)
	ctx := context.Background()
	org, repo, err := f.c.Slugs(ctx, f.website.ID)
	if err != nil || org != "acme" || repo != "website" {
		t.Errorf("Slugs() = %q, %q, %v, want acme, website", org, repo, err)
	}
	if _, _, err := f.c.Slugs(ctx, uuid.New()); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Slugs() of no repository: %v, want repos.ErrNotFound", err)
	}
}
