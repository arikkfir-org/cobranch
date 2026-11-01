package gitserver_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/git"
)

// websiteURL is Acme's website on base, as user's personal token reaches it.
func (s *stack) websiteURL(base string, user uuid.UUID) string {
	return s.url(base, s.token(user), "acme/website.git")
}

func TestCloneAndPush(t *testing.T) {
	s := newStack(t)
	dir := clone(t, s.websiteURL(s.base, olivia))
	first := commit(t, dir, "README.md", "# Website\n")
	mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")

	if got := query[string](t, s.pool, "SELECT sha FROM refs WHERE repo_id = $1 AND name = 'refs/heads/main'", website); got != first {
		t.Errorf("main = %s, want %s", got, first)
	}
	if got := query[string](t, s.pool, "SELECT head FROM branches WHERE repo_id = $1 AND name = 'main' AND deleted_at IS NULL", website); got != first {
		t.Errorf("branch main is at %s, want %s", got, first)
	}
	if got := query[uuid.UUID](t, s.pool, "SELECT pusher_id FROM pushes WHERE repo_id = $1", website); got != olivia {
		t.Errorf("the pusher = %s, want Olivia", got)
	}
	// The push returned with its commit recorded, and waits for the push worker to name who wrote it.
	if got := query[int](t, s.pool, "SELECT count(*) FROM commits WHERE repo_id = $1 AND sha = $2 AND author_id IS NULL AND committer_id IS NULL", website, first); got != 1 {
		t.Error("the commit isn't recorded, unattributed, once the push returned")
	}
	if got := query[int](t, s.pool, "SELECT count(*) FROM pushes WHERE repo_id = $1 AND processed_at IS NULL", website); got != 1 {
		t.Error("the push doesn't wait for the push worker")
	}
	s.process()
	checkCommit(t, s, first)

	// A second Git server, with an empty cache, serves the push from the bucket.
	other := clone(t, s.websiteURL(s.serve(t.TempDir()), gwen))
	if got := strings.TrimSpace(mustGit(t, other, "rev-parse", "HEAD")); got != first {
		t.Errorf("a fresh clone is at %s, want %s", got, first)
	}
	second := commit(t, dir, "index.html", "<h1>Acme</h1>\n")
	mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
	mustGit(t, other, "pull", "-q", "--ff-only")
	if got := strings.TrimSpace(mustGit(t, other, "rev-parse", "HEAD")); got != second {
		t.Errorf("a fetch brought %s, want %s", got, second)
	}
	if got := query[int](t, s.pool, "SELECT count(*) FROM packs WHERE repo_id = $1 AND state = 'active'", website); got != 2 {
		t.Errorf("%d active packs, want one per push", got)
	}
}

// checkCommit checks that sha is recorded as the commit its test made: written by Ada, whom nobody knew, so she is
// now a login-less user holding her email unverified, and committed by Olivia.
func checkCommit(t *testing.T, s *stack, sha string) {
	t.Helper()
	var author, committer uuid.UUID
	var email, zone string
	var at time.Time
	if err := s.pool.QueryRow(context.Background(), "SELECT author_id, committer_id, author_email, author_zone, authored_at FROM commits WHERE repo_id = $1 AND sha = $2",
		website, sha).Scan(&author, &committer, &email, &zone, &at); err != nil {
		t.Fatal(err)
	}
	if committer != olivia || email != "Ada@Example.com" || zone != "+0200" || !at.Equal(time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("commit = committer %s, author %s at %v %s, want Olivia, and Ada's email as written at 10:00 UTC in +0200", committer, email, at, zone)
	}
	var verified, signsIn bool
	if err := s.pool.QueryRow(context.Background(), "SELECT e.verified, EXISTS (SELECT 1 FROM identities i WHERE i.principal_id = e.principal_id) FROM principal_emails e WHERE e.email = 'ada@example.com' AND e.principal_id = $1",
		author).Scan(&verified, &signsIn); err != nil {
		t.Fatalf("Ada's principal holds no email ada@example.com: %v", err)
	}
	if verified || signsIn {
		t.Errorf("Ada's email verified %t, signs in %t; want a login-less user holding it unverified", verified, signsIn)
	}
}

func TestTagsAndDeletes(t *testing.T) {
	s := newStack(t)
	dir := clone(t, s.websiteURL(s.base, olivia))
	head := commit(t, dir, "README.md", "# Website\n")
	mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main", "HEAD:refs/heads/feature")
	mustGit(t, dir, "tag", "-a", "v1", "-m", "The first")
	mustGit(t, dir, "push", "-q", "origin", "v1")
	if got := query[int](t, s.pool, "SELECT count(*) FROM commits WHERE repo_id = $1", website); got != 1 {
		t.Errorf("%d commits, want the tag to bring none", got)
	}
	if got := query[string](t, s.pool, "SELECT sha FROM refs WHERE repo_id = $1 AND name = 'refs/tags/v1'", website); got == head || got == "" {
		t.Errorf("v1 = %q, want the tag's object", got)
	}
	mustGit(t, dir, "push", "-q", "origin", ":refs/heads/feature")
	if got := query[int](t, s.pool, "SELECT count(*) FROM refs WHERE repo_id = $1 AND name = 'refs/heads/feature'", website); got != 0 {
		t.Error("feature is still a ref after its deletion")
	}
	if got := query[int](t, s.pool, "SELECT count(*) FROM branches WHERE repo_id = $1 AND name = 'feature' AND deleted_at IS NOT NULL", website); got != 1 {
		t.Error("branch feature is not marked deleted")
	}
}

func TestRefusedPushes(t *testing.T) {
	moved := func(ctx context.Context, push git.Push, next git.Store) error {
		// Another push moves main first, as one that commits between this push's snapshot and its record would.
		if err := next.Record(ctx, git.Push{ID: uuid.Must(uuid.NewV7()), RepoID: push.RepoID, PusherID: olivia, Transport: git.TransportHTTPS,
			Updates: []git.RefUpdate{{Name: "refs/heads/main", Old: push.Updates[0].Old, New: strings.Repeat("a", 40)}}, ReceivedAt: time.Now()}); err != nil {
			return fmt.Errorf("moving main first: %w", err)
		}
		return next.Record(ctx, push)
	}
	failing := func(context.Context, git.Push, git.Store) error {
		return errors.New("the database is down")
	}
	tests := []struct {
		name, ref string
		record    func(ctx context.Context, push git.Push, next git.Store) error
		want      string
	}{
		{name: "a note", ref: "refs/notes/commits", want: "Cobranch keeps branches and tags only: refs/notes/commits refused"},
		{name: "a branch under cobranch/", ref: "refs/heads/cobranch/settings", want: "Cobranch writes refs under cobranch/ itself"},
		{name: "a tag under cobranch/", ref: "refs/tags/cobranch/v1", want: "Cobranch writes refs under cobranch/ itself"},
		{name: "a ref another push moved", ref: "refs/heads/main", record: moved, want: "refs/heads/main moved since you fetched: fetch, merge or rebase, then push again"},
		{name: "a database that fails", ref: "refs/heads/main", record: failing, want: git.MessageNotRecorded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStack(t)
			dir := clone(t, s.websiteURL(s.base, olivia))
			commit(t, dir, "README.md", "# Website\n")
			mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
			commit(t, dir, "index.html", "<h1>Acme</h1>\n")
			s.record = tt.record
			out, err := gitCmd(t, dir, "push", "origin", "HEAD:"+tt.ref)
			if err == nil || !strings.Contains(out, "remote: "+tt.want) {
				t.Fatalf("git push = %v:\n%s\nwant it refused with %q", err, out, tt.want)
			}
			if got := query[int](t, s.pool, "SELECT count(*) FROM commits WHERE repo_id = $1", website); got != 1 {
				t.Errorf("%d commits recorded, want only the first push's", got)
			}
		})
	}
}

func TestLostAnswer(t *testing.T) {
	s := newStack(t)
	var calls atomic.Int32
	s.record = func(ctx context.Context, push git.Push, next git.Store) error {
		if err := next.Record(ctx, push); err != nil {
			return fmt.Errorf("recording: %w", err)
		}
		if calls.Add(1) > 1 {
			return nil
		}
		return errors.New("the push committed, and the answer is lost on the way") // as a connection that drops at COMMIT
	}
	dir := clone(t, s.websiteURL(s.base, olivia))
	commit(t, dir, "README.md", "# Website\n")
	mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
	if got := calls.Load(); got != 2 {
		t.Errorf("the Git server recorded the push %d times, want twice", got)
	}
	if got := query[int](t, s.pool, "SELECT count(*) FROM pushes WHERE repo_id = $1", website); got != 1 {
		t.Errorf("%d pushes recorded, want one", got)
	}
}

func TestConcurrentPushes(t *testing.T) {
	s := newStack(t)
	base := clone(t, s.websiteURL(s.base, olivia))
	commit(t, base, "README.md", "# Website\n")
	mustGit(t, base, "push", "-q", "origin", "HEAD:refs/heads/main")
	clones := []string{clone(t, s.websiteURL(s.base, olivia)), clone(t, s.websiteURL(s.base, olivia))}
	pushAll := func(ref func(i int) string) []error {
		errs := make([]error, len(clones))
		var wg sync.WaitGroup
		for i, dir := range clones {
			commit(t, dir, "file.txt", "from clone "+string(rune('a'+i))+" to "+ref(i)+"\n")
			wg.Go(func() {
				out, err := gitCmd(t, dir, "push", "origin", "HEAD:"+ref(i))
				if err != nil {
					err = errors.New(out)
				}
				errs[i] = err
			})
		}
		wg.Wait()
		return errs
	}
	for i, err := range pushAll(func(i int) string { return "refs/heads/branch-" + string(rune('a'+i)) }) {
		if err != nil {
			t.Errorf("pushing branch %d alongside another: %v", i, err)
		}
	}
	accepted := 0
	for _, err := range pushAll(func(int) string { return "refs/heads/main" }) {
		if err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Errorf("%d of two pushes into main at once were accepted, want one", accepted)
	}
}

// TestPushAgain pushes a branch, deletes it and pushes it again: the second push of the branch brings the same
// objects, in a pack of the same name, which the repository lists already.
func TestPushAgain(t *testing.T) {
	s := newStack(t)
	dir := clone(t, s.websiteURL(s.base, olivia))
	commit(t, dir, "README.md", "# Website\n")
	mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
	feature := commit(t, dir, "index.html", "<h1>Acme</h1>\n")
	mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/feature")
	mustGit(t, dir, "push", "-q", "origin", ":refs/heads/feature")
	if out, err := gitCmd(t, dir, "push", "origin", "HEAD:refs/heads/feature"); err != nil {
		t.Fatalf("pushing feature again: %v\n%s", err, out)
	}
	if got := query[string](t, s.pool, "SELECT sha FROM refs WHERE repo_id = $1 AND name = 'refs/heads/feature'", website); got != feature {
		t.Errorf("feature = %s, want %s", got, feature)
	}
	other := clone(t, s.websiteURL(s.serve(t.TempDir()), gwen))
	mustGit(t, other, "checkout", "-q", "feature")
	if got := strings.TrimSpace(mustGit(t, other, "rev-parse", "HEAD")); got != feature {
		t.Errorf("a fresh clone's feature is at %s, want %s", got, feature)
	}
}
