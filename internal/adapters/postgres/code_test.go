package postgres

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

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

func TestCodeStoreFirstParents(t *testing.T) {
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

func TestInternalTokens(t *testing.T) {
	f := newCodeFixture(t)
	tokens := NewInternalTokens(f.pool)
	first, err := tokens.Read(context.Background(), "git-server")
	if err != nil || len(first) != 64 || strings.Trim(first, "0123456789abcdef") != "" {
		t.Errorf("Read(git-server) = %q, %v, want 64 hexadecimal digits", first, err)
	}
	other := newCodeFixture(t)
	if second, err := NewInternalTokens(other.pool).Read(context.Background(), "git-server"); err != nil || second == first {
		t.Errorf("another database's token = %q, %v, want one of its own", second, err)
	}
	if _, err := tokens.Read(context.Background(), "nothing"); err == nil {
		t.Error("Read(nothing) found a token")
	}
}
