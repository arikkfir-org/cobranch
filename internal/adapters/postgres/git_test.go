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

// process runs the push worker's part until no push waits, and returns how many pushes it processed.
func (f *gitFixture) process(t *testing.T) int {
	t.Helper()
	for n := 0; ; n++ {
		id, err := f.g.Process(context.Background(), git.RetryAfter)
		if err != nil {
			t.Fatalf("Process() = %v", err)
		}
		if id == uuid.Nil {
			return n
		}
	}
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
	// The push waits for the push worker, which alone maps its commits to principals.
	if got := count(t, f.pool, "SELECT count(*) FROM pushes WHERE id = $1 AND processed_at IS NULL AND attempts = 0", first.ID); got != 1 {
		t.Error("the push doesn't wait for the push worker")
	}
	if got := count(t, f.pool, "SELECT count(*) FROM commits WHERE author_id IS NOT NULL OR committer_id IS NOT NULL"); got != 0 {
		t.Errorf("%d commits name principals before the push worker processed their push", got)
	}
	if n := f.process(t); n != 1 {
		t.Errorf("the push worker processed %d pushes, want 1", n)
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
		"SELECT count(*) FROM pushes WHERE repo_id = $1 AND processed_at IS NOT NULL":               1,
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
	f.process(t)
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
	f.process(t)
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
	f.process(t)
	if got := count(t, f.pool, "SELECT count(DISTINCT author_id) FROM commits WHERE author_email = 'bob@example.org'"); got != 1 {
		t.Errorf("Bob's commits name %d principals, want one", got)
	}
}

func TestGitStoreLoginlessAtOnce(t *testing.T) {
	f := newGitFixture(t)
	ctx := context.Background()
	for _, repo := range []uuid.UUID{f.website.ID, f.secret.ID} {
		if err := f.g.Record(ctx, pushOf(repo, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}},
			commit(shaA, person("New", "new@example.org")))); err != nil {
			t.Fatal(err)
		}
	}
	// Two push workers each claim one of the pushes, and both make the same email's login-less user.
	var wg sync.WaitGroup
	ids := make([]uuid.UUID, 2)
	errs := make([]error, 2)
	for i := range ids {
		wg.Go(func() { ids[i], errs[i] = f.g.Process(ctx, git.RetryAfter) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("push worker %d: %v", i, err)
		}
	}
	f.process(t)
	if got := count(t, f.pool, "SELECT count(DISTINCT author_id) FROM commits WHERE author_email = 'new@example.org' AND author_id IS NOT NULL"); got != 1 {
		t.Errorf("two pushes at once made %d login-less users for one email, want one", got)
	}
	if got := count(t, f.pool, "SELECT count(*) FROM commits WHERE author_email = 'new@example.org' AND author_id IS NULL"); got != 0 {
		t.Errorf("%d commits name no author", got)
	}
}

// recordTwo records Ada's first push into website, then her second into secret, and returns them.
func (f *gitFixture) recordTwo(t *testing.T) (first, second git.Push) {
	t.Helper()
	first = pushOf(f.website.ID, f.olivia, "pack-1", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}},
		commit(shaA, person("Ada Author", "ada@example.com")))
	second = pushOf(f.secret.ID, f.olivia, "pack-2", []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: shaA}},
		commit(shaA, person("Ada Author", "ada@example.com")))
	for _, p := range []git.Push{first, second} {
		if err := f.g.Record(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	return first, second
}

func TestGitStoreProcessSkipsHeld(t *testing.T) {
	f := newGitFixture(t)
	ctx := context.Background()
	if n := f.process(t); n != 0 {
		t.Fatalf("Process() with nothing recorded processed %d pushes", n)
	}
	first, second := f.recordTwo(t)
	// A push another worker holds is skipped, not waited for.
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT id FROM pushes WHERE id = $1 FOR UPDATE", first.ID); err != nil {
		t.Fatal(err)
	}
	if id, err := f.g.Process(ctx, git.RetryAfter); err != nil || id != second.ID {
		t.Errorf("Process() while another worker holds the first push = %s, %v, want the second push", id, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if id, err := f.g.Process(ctx, git.RetryAfter); err != nil || id != first.ID {
		t.Errorf("Process() once the first push is free = %s, %v, want the first push", id, err)
	}
}

func TestGitStoreProcessRetries(t *testing.T) {
	f := newGitFixture(t)
	ctx := context.Background()
	if _, err := f.pool.Exec(ctx, "CREATE FUNCTION refuse() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'the disk is full'; END $$; "+
		"CREATE TRIGGER refuse BEFORE UPDATE ON commits FOR EACH ROW EXECUTE FUNCTION refuse()"); err != nil {
		t.Fatal(err)
	}
	first, _ := f.recordTwo(t)
	// A push that fails keeps waiting, with its error, until its retry; what it did is rolled back.
	var attempts []int
	retry := func(n int) time.Duration {
		attempts = append(attempts, n)
		return time.Hour
	}
	if id, err := f.g.Process(ctx, retry); id != first.ID || err == nil || !strings.Contains(err.Error(), "the disk is full") {
		t.Errorf("Process() of a push that fails = %s, %v, want the first push and its error", id, err)
	}
	if got := count(t, f.pool, "SELECT count(*) FROM pushes WHERE id = $1 AND processed_at IS NULL AND attempts = 1 "+
		"AND error LIKE '%the disk is full%' AND retry_at > now() + interval '59 minutes'", first.ID); got != 1 {
		t.Error("the failed push doesn't wait for its retry with its error")
	}
	if got := count(t, f.pool, "SELECT count(*) FROM principal_emails WHERE email = 'ada@example.com'"); got != 0 {
		t.Errorf("the failed push left %d login-less users", got)
	}
	if _, err := f.pool.Exec(ctx, "DROP TRIGGER refuse ON commits"); err != nil {
		t.Fatal(err)
	}
	// The second push still waits; the first waits for its retry, and once it comes, it's processed and its error cleared.
	if n := f.process(t); n != 1 {
		t.Errorf("Process() before the first push's retry processed %d pushes, want the second", n)
	}
	if _, err := f.pool.Exec(ctx, "UPDATE pushes SET retry_at = now() WHERE id = $1", first.ID); err != nil {
		t.Fatal(err)
	}
	if id, err := f.g.Process(ctx, retry); err != nil || id != first.ID || len(attempts) != 1 || attempts[0] != 1 {
		t.Errorf("Process() at the retry = %s, %v after asking when to retry at attempts %v, want the first push after [1]", id, err, attempts)
	}
	if got := count(t, f.pool, "SELECT count(*) FROM pushes WHERE id = $1 AND processed_at IS NOT NULL AND attempts = 1 AND error = ''", first.ID); got != 1 {
		t.Error("the retried push isn't processed")
	}
	if got := count(t, f.pool, "SELECT count(*) FROM commits WHERE repo_id = $1 AND author_id IS NOT NULL", f.website.ID); got != 1 {
		t.Error("the retried push's commit has no author")
	}
}

func TestListenPushes(t *testing.T) {
	f := newGitFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wake := ListenPushes(ctx, f.pool)
	waitWake := func(why string) {
		t.Helper()
		select {
		case <-wake:
		case <-time.After(10 * time.Second):
			t.Fatalf("no wake %s", why)
		}
	}
	waitWake("once it listens")
	if err := f.g.Record(ctx, pushOf(f.website.ID, f.olivia, "", []git.RefUpdate{{Name: "refs/tags/v1", Old: git.ZeroSHA, New: shaA}})); err != nil {
		t.Fatal(err)
	}
	waitWake("after a push")
	// Losing its connection, it listens on another, and wakes for what it may have missed.
	if _, err := f.pool.Exec(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid() AND query LIKE '%LISTEN cobranch_pushes%'"); err != nil {
		t.Fatal(err)
	}
	waitWake("after it lost its connection")
	if err := f.g.Record(ctx, pushOf(f.website.ID, f.olivia, "", []git.RefUpdate{{Name: "refs/tags/v2", Old: git.ZeroSHA, New: shaA}})); err != nil {
		t.Fatal(err)
	}
	waitWake("after a push on its new connection")
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
	f.process(t)
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
