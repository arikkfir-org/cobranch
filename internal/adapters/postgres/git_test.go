package postgres

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/git"
)

var (
	shaA = strings.Repeat("a", 40)
	shaB = strings.Repeat("b", 40)
	shaC = strings.Repeat("c", 40)
)

// gitFixture is repoFixture with Git hosting's store.
type gitFixture struct {
	*repoFixture
	g *GitStore
}

func newGitFixture(t *testing.T) *gitFixture {
	t.Helper()
	f := &gitFixture{repoFixture: newRepoFixture(t)}
	f.g = NewGitStore(f.pool)
	return f
}

// commit is a commit of sha's, by author, committed by Olivia.
func commit(sha string, author git.Person, parents ...string) git.Commit {
	return git.Commit{SHA: sha, Tree: shaC, Parents: parents, Message: "Change " + sha[:1] + "\n", Author: author,
		Committer: git.Person{Name: "Olivia", Email: "olivia@example.com", At: now, Zone: "+0000"}}
}

func person(name, email string) git.Person {
	return git.Person{Name: name, Email: email, At: now.Add(-time.Hour), Zone: "+0200"}
}

// pushOf is Olivia's push into repo of updates, bringing pack (when not "") and commits.
func pushOf(repo uuid.UUID, pusher uuid.UUID, pack string, updates []git.RefUpdate, commits ...git.Commit) git.Push {
	p := git.Push{ID: uuid.Must(uuid.NewV7()), RepoID: repo, PusherID: pusher, Transport: git.TransportHTTPS, Updates: updates, Commits: commits, ReceivedAt: now}
	if pack != "" {
		p.Pack = &git.Pack{Name: pack, Size: 100, Objects: int64(len(commits)) * 3}
	}
	return p
}

func TestGitStoreRecord(t *testing.T) {
	f := newGitFixture(t)
	ctx := context.Background()
	first := pushOf(f.website.ID, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}},
		commit(shaA, person("Ada Author", "Ada@Example.com")))
	if err := f.g.Record(ctx, first); err != nil {
		t.Fatal(err)
	}
	// The Git server asks again when it lost the answer: nothing happens twice.
	if err := f.g.Record(ctx, first); err != nil {
		t.Fatalf("Record() of a recorded push = %v", err)
	}
	snapshot, err := f.g.Snapshot(ctx, f.website.ID)
	if err != nil || len(snapshot.Refs) != 1 || snapshot.Refs[0] != (git.Ref{Name: "refs/heads/main", SHA: shaA}) || len(snapshot.Packs) != 1 || snapshot.Packs[0] != "pack-1" {
		t.Errorf("Snapshot() = %+v, %v", snapshot, err)
	}
	for query, want := range map[string]int{
		"SELECT count(*) FROM pushes WHERE repo_id = $1 AND pusher_id IS NOT NULL":                  1,
		"SELECT count(*) FROM commits WHERE repo_id = $1":                                           1,
		"SELECT count(*) FROM branches WHERE repo_id = $1 AND name = 'main' AND deleted_at IS NULL": 1,
		"SELECT count(*) FROM events WHERE kind = 'repo.pushed' AND subject_id = $1":                1,
		"SELECT count(*) FROM packs WHERE repo_id = $1 AND state = 'active' AND objects = 3":        1,
	} {
		if got := count(t, f.pool, query, f.website.ID); got != want {
			t.Errorf("%s = %d, want %d", query, got, want)
		}
	}
	var author, committer *uuid.UUID
	if err := f.pool.QueryRow(ctx, "SELECT author_id, committer_id FROM commits WHERE sha = $1", shaA).Scan(&author, &committer); err != nil {
		t.Fatal(err)
	}
	if committer == nil || *committer != f.olivia {
		t.Errorf("the committer is %v, want Olivia", committer)
	}
	if author == nil || count(t, f.pool, "SELECT count(*) FROM principals p JOIN principal_emails e ON e.principal_id = p.id "+
		"WHERE p.id = $1 AND p.display_name = 'Ada Author' AND e.email = 'ada@example.com' AND NOT e.verified", *author) != 1 {
		t.Errorf("the author is %v, want a login-less Ada holding ada@example.com unverified", author)
	}
}

func TestGitStoreRecordStale(t *testing.T) {
	f := newGitFixture(t)
	ctx := context.Background()
	create := []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}}
	if err := f.g.Record(ctx, pushOf(f.website.ID, f.olivia, "pack-1", create, commit(shaA, person("Olivia", "olivia@example.com")))); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		updates []git.RefUpdate
		want    string
	}{
		{name: "creating a ref that exists", updates: create, want: "refs/heads/main"},
		{name: "moving a ref from where it isn't", updates: []git.RefUpdate{{Name: "refs/heads/main", Old: shaB, New: shaC}}, want: "refs/heads/main"},
		{name: "deleting a ref that isn't there", updates: []git.RefUpdate{{Name: "refs/heads/main", Old: shaA, New: shaB}, {Name: "refs/heads/gone", Old: shaA, New: git.ZeroSHA}},
			want: "refs/heads/gone"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stale *git.StaleError
			err := f.g.Record(ctx, pushOf(f.website.ID, f.olivia, "pack-2", tt.updates, commit(shaB, person("Olivia", "olivia@example.com"), shaA)))
			if !errors.As(err, &stale) || stale.Ref != tt.want {
				t.Fatalf("Record() = %v, want %s stale", err, tt.want)
			}
			var left int
			if err := f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM pushes WHERE repo_id = $1) + (SELECT count(*) FROM packs WHERE repo_id = $1)",
				f.website.ID).Scan(&left); err != nil || left != 2 {
				t.Errorf("a refused push left %d pushes and packs (%v), want the first push's 2", left, err)
			}
		})
	}
	if err := f.g.Record(ctx, pushOf(uuid.New(), f.olivia, "", create)); !errors.Is(err, git.ErrNotFound) {
		t.Errorf("Record() into no repository = %v, want %v", err, git.ErrNotFound)
	}
}

func TestGitStoreBranches(t *testing.T) {
	f := newGitFixture(t)
	ctx := context.Background()
	steps := []git.Push{
		pushOf(f.website.ID, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}, {Name: "refs/heads/feature", Old: git.ZeroSHA, New: shaA}},
			commit(shaA, person("Olivia", "olivia@example.com"))),
		pushOf(f.website.ID, f.max, "pack-2", []git.RefUpdate{{Name: "refs/heads/main", Old: shaA, New: shaB}, {Name: "refs/tags/v1", Old: git.ZeroSHA, New: shaA}},
			commit(shaB, person("Max", "max@example.com"), shaA)),
		pushOf(f.website.ID, f.olivia, "", []git.RefUpdate{{Name: "refs/heads/feature", Old: shaA, New: git.ZeroSHA}}),
		pushOf(f.website.ID, f.olivia, "", []git.RefUpdate{{Name: "refs/heads/feature", Old: git.ZeroSHA, New: shaB}}),
	}
	for i, p := range steps {
		if err := f.g.Record(ctx, p); err != nil {
			t.Fatalf("push %d: %v", i, err)
		}
	}
	snapshot, err := f.g.Snapshot(ctx, f.website.ID)
	want := []git.Ref{{Name: "refs/heads/feature", SHA: shaB}, {Name: "refs/heads/main", SHA: shaB}, {Name: "refs/tags/v1", SHA: shaA}}
	if err != nil || len(snapshot.Refs) != len(want) || len(snapshot.Packs) != 2 || snapshot.Packs[0] != "pack-1" {
		t.Fatalf("Snapshot() = %+v, %v", snapshot, err)
	}
	for i := range want {
		if snapshot.Refs[i] != want[i] {
			t.Errorf("ref %d = %+v, want %+v", i, snapshot.Refs[i], want[i])
		}
	}
	for query, wantCount := range map[string]int{
		// feature lived, was deleted and lives again as another branch; a tag is no branch.
		"SELECT count(*) FROM branches WHERE repo_id = $1 AND name = 'feature'":                                                  2,
		"SELECT count(*) FROM branches WHERE repo_id = $1 AND name = 'feature' AND deleted_at IS NULL AND head = '" + shaB + "'": 1,
		"SELECT count(*) FROM branches WHERE repo_id = $1 AND name = 'main' AND head = '" + shaB + "'":                           1,
		"SELECT count(*) FROM branches WHERE repo_id = $1 AND name LIKE '%v1%'":                                                  0,
		"SELECT count(*) FROM pushes WHERE repo_id = $1 AND pack = ''":                                                           2,
	} {
		if got := count(t, f.pool, query, f.website.ID); got != wantCount {
			t.Errorf("%s = %d, want %d", query, got, wantCount)
		}
	}
	if got := count(t, f.pool, "SELECT count(*) FROM commits WHERE sha = $1 AND author_id = $2", shaB, f.max); got != 1 {
		t.Error("Max's commit is not his")
	}
}

func TestGitStorePrincipals(t *testing.T) {
	f := newGitFixture(t)
	ctx := context.Background()
	// Max also holds an old address unverified: he signs in, so a commit can't claim him by it.
	if _, err := f.pool.Exec(ctx, "INSERT INTO principal_emails (email, principal_id, verified) VALUES ('max@old.example.com', $1, false)", f.max); err != nil {
		t.Fatal(err)
	}
	commits := []git.Commit{
		commit(shaA, person("Nia", "NIA@example.com")),
		commit(shaB, person("Max", "max@old.example.com"), shaA),
		commit(shaC, person("", ""), shaB),
		commit(strings.Repeat("d", 40), person("", "bob@example.org"), shaC),
		commit(strings.Repeat("e", 40), person(strings.Repeat("é", 120), "long@example.org"), shaC),
	}
	if err := f.g.Record(ctx, pushOf(f.website.ID, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: commits[3].SHA}}, commits...)); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, sha, author string
	}{
		{name: "a user's verified email, in any case", sha: shaA, author: f.nia.String()},
		{name: "an email a user who signs in holds unverified", sha: shaB, author: ""},
		{name: "no email", sha: shaC, author: ""},
	}
	for _, tt := range tests {
		var author *uuid.UUID
		if err := f.pool.QueryRow(ctx, "SELECT author_id FROM commits WHERE sha = $1", tt.sha).Scan(&author); err != nil {
			t.Fatal(err)
		}
		got := ""
		if author != nil {
			got = author.String()
		}
		if got != tt.author {
			t.Errorf("%s: the author is %q, want %q", tt.name, got, tt.author)
		}
	}
	for email, name := range map[string]string{"bob@example.org": "bob", "long@example.org": strings.Repeat("é", 100)} {
		if got := count(t, f.pool, "SELECT count(*) FROM principals p JOIN principal_emails e ON e.principal_id = p.id WHERE e.email = $1 AND p.display_name = $2", email, name); got != 1 {
			t.Errorf("no login-less user %q holds %s", name, email)
		}
	}
	// A later push's commit by Bob is the same login-less user's.
	if err := f.g.Record(ctx, pushOf(f.secret.ID, f.olivia, "pack-2", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}},
		commit(shaA, person("Bob B.", "bob@example.org")))); err != nil {
		t.Fatal(err)
	}
	if got := count(t, f.pool, "SELECT count(DISTINCT author_id) FROM commits WHERE author_email = 'bob@example.org'"); got != 1 {
		t.Errorf("Bob's commits name %d principals, want one", got)
	}
}

func TestGitStoreLoginlessAtOnce(t *testing.T) {
	f := newGitFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, repo := range []uuid.UUID{f.website.ID, f.secret.ID} {
		wg.Go(func() {
			errs[i] = f.g.Record(ctx, pushOf(repo, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}},
				commit(shaA, person("New", "new@example.org"))))
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("push %d: %v", i, err)
		}
	}
	if got := count(t, f.pool, "SELECT count(DISTINCT author_id) FROM commits WHERE author_email = 'new@example.org' AND author_id IS NOT NULL"); got != 1 {
		t.Errorf("two pushes at once made %d login-less users for one email, want one", got)
	}
}

func TestLoginlessName(t *testing.T) {
	tests := []struct{ name, email, want string }{
		{name: " Ada Author ", email: "ada@example.com", want: "Ada Author"},
		{name: "", email: "bob@example.org", want: "bob"},
		{name: strings.Repeat("x", 101), email: "x@example.org", want: strings.Repeat("x", 100)},
	}
	for _, tt := range tests {
		if got := loginlessName(tt.name, tt.email); got != tt.want {
			t.Errorf("loginlessName(%q, %q) = %q, want %q", tt.name, tt.email, got, tt.want)
		}
	}
}

func TestAuthStoreAdopt(t *testing.T) {
	f := newGitFixture(t)
	ctx := context.Background()
	if err := f.g.Record(ctx, pushOf(f.website.ID, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}},
		commit(shaA, person("ali", "alice@example.com")))); err != nil {
		t.Fatal(err)
	}
	s := NewAuthStore(f.pool)
	loginless, err := s.LoginlessHolder(ctx, "alice@example.com")
	if err != nil {
		t.Fatalf("LoginlessHolder() = %v", err)
	}
	if _, err := s.LoginlessHolder(ctx, "nia@example.com"); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("LoginlessHolder() of a user who signs in = %v, want %v", err, auth.ErrNotFound)
	}
	up := signUp("https://idp.example.com", "alice", "alice@example.com", true)
	up.Principal.ID, up.Identity.UserID, up.Adopt = loginless, loginless, true
	if err := s.SignUp(ctx, up); err != nil {
		t.Fatalf("SignUp() adopting = %v", err)
	}
	if holder, _, err := s.EmailHolder(ctx, "alice@example.com"); err != nil || holder != loginless {
		t.Errorf("EmailHolder() = %s, %v, want the adopted user", holder, err)
	}
	if got := count(t, f.pool, "SELECT count(*) FROM commits c JOIN principals p ON p.id = c.author_id WHERE c.sha = $1 AND p.id = $2 AND p.display_name = 'Alice'", shaA, loginless); got != 1 {
		t.Error("the commit is not the adopted Alice's")
	}
	if _, err := s.LoginlessHolder(ctx, "alice@example.com"); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("LoginlessHolder() after the adoption = %v, want %v", err, auth.ErrNotFound)
	}
	stranger := signUp("https://idp.example.com", "bob", "bob@example.com", true)
	stranger.Principal.ID, stranger.Identity.UserID, stranger.Adopt = f.nia, f.nia, true
	if err := s.SignUp(ctx, stranger); err == nil {
		t.Error("SignUp() adopting a user who doesn't hold the email succeeded")
	}
}
