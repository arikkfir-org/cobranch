package gitserver_test

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/adapters/postgres"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/limits"
)

// files counts the files under dir.
func files(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	if err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestPushOverGitDataLimit pushes into Acme's website under Acme's Git data limit: a pack the limit leaves no room
// for is refused before the Git server stores it, and one another push filled the room for meanwhile is refused as
// the push is recorded.
func TestPushOverGitDataLimit(t *testing.T) {
	filled := func(ctx context.Context, push git.Push, next git.Store) error {
		// Another push stores the organization's whole limit first, between this push's weighing and its record.
		other := git.Push{ID: uuid.Must(uuid.NewV7()), RepoID: push.RepoID, OrgID: push.OrgID, PusherID: olivia, Transport: git.TransportHTTPS,
			Updates: []git.RefUpdate{{Name: "refs/tags/other", Old: git.ZeroSHA, New: strings.Repeat("a", 40)}}, ReceivedAt: time.Now(),
			Pack: &git.Pack{Name: "pack-" + strings.Repeat("b", 40), Upload: uuid.Must(uuid.NewV7()), Size: 1 << 20, Objects: 1}}
		if err := next.Record(ctx, other); err != nil {
			return fmt.Errorf("filling the limit first: %w", err)
		}
		return next.Record(ctx, push)
	}
	tests := []struct {
		name   string
		limit  int64
		record func(ctx context.Context, push git.Push, next git.Store) error
		want   string
		// wantStored is whether the refused push's pack reaches the object store, unlisted, for maintenance to delete.
		wantStored bool
	}{
		{name: "a pack over the limit", limit: 64, want: "This organization stores 0 B of Git data, and this push adds "},
		{name: "a pack another push left no room for", limit: 1 << 20, record: filled,
			want: "This organization stores 1 MiB of Git data, and this push adds ", wantStored: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStackIn(t, inFiles)
			if _, err := s.pool.Exec(context.Background(), "UPDATE installation_settings SET git_data_per_organization = $1", tt.limit); err != nil {
				t.Fatal(err)
			}
			s.quotas = limits.NewService(postgres.NewLimitStore(s.pool), limits.Settings{})
			s.record = tt.record
			dir := clone(t, s.websiteURL(s.serve(t.TempDir()), olivia))
			commit(t, dir, "README.md", "# Website\n")
			before := files(t, s.bucketDir)
			out, err := gitCmd(t, dir, "push", "origin", "HEAD:refs/heads/main")
			if err == nil || !strings.Contains(out, "remote: "+tt.want) || !strings.Contains(out, "Nothing changed; an administrator can raise the limit.") {
				t.Fatalf("git push = %v:\n%s\nwant it refused with %q", err, out, tt.want)
			}
			if got := query[int](t, s.pool, "SELECT count(*) FROM refs WHERE repo_id = $1 AND name = 'refs/heads/main'", website); got != 0 {
				t.Error("main moved, want the refused push to move nothing")
			}
			if stored := files(t, s.bucketDir) > before; stored != tt.wantStored {
				t.Errorf("the refused pack stored %t, want %t", stored, tt.wantStored)
			}
		})
	}
}

// TestPushOfAPackListedAlready pushes objects into Acme's website again, at its Git data limit, after the branch that
// brought them is deleted: the pack the push brings is the one the repository lists already, so it adds nothing and is
// taken, before the Git server stores it and as the push is recorded.
func TestPushOfAPackListedAlready(t *testing.T) {
	s := newStackIn(t, inFiles)
	s.quotas = limits.NewService(postgres.NewLimitStore(s.pool), limits.Settings{})
	dir := clone(t, s.websiteURL(s.serve(t.TempDir()), olivia))
	commit(t, dir, "README.md", "# Website\n")
	mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/feature")
	used := query[int64](t, s.pool, "SELECT coalesce(sum(size), 0)::bigint FROM packs WHERE org_id = (SELECT org_id FROM repositories WHERE id = $1) AND state = 'active'", website)
	if _, err := s.pool.Exec(context.Background(), "UPDATE installation_settings SET git_data_per_organization = $1", used); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "push", "-q", "origin", ":refs/heads/feature")
	if out, err := gitCmd(t, dir, "push", "origin", "HEAD:refs/heads/again"); err != nil {
		t.Fatalf("git push of the same objects again = %v:\n%s\nwant it taken: its pack adds nothing", err, out)
	}
	if got := query[int](t, s.pool, "SELECT count(*) FROM packs WHERE repo_id = $1 AND state = 'active'", website); got != 1 {
		t.Errorf("website lists %d active packs, want the one", got)
	}
}
