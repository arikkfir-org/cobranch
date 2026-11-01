package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/git"
)

// GitStore keeps Git hosting's rows (db/migrations/00012_git.sql): git.Store on PostgreSQL, through the queries sqlc
// generates from db/queries.
type GitStore struct {
	pool *pgxpool.Pool
	q    *queries.Queries
}

// NewGitStore returns Git hosting's store on pool's database.
func NewGitStore(pool *pgxpool.Pool) *GitStore {
	return &GitStore{pool: pool, q: queries.New(pool)}
}

var _ git.Store = (*GitStore)(nil)

// Snapshot reads the repository's refs and active packs in one statement; a repository with neither, or none at all,
// has an empty one.
func (s *GitStore) Snapshot(ctx context.Context, repoID uuid.UUID) (git.Snapshot, error) {
	row, err := s.q.Snapshot(ctx, repoID)
	if err != nil {
		return git.Snapshot{}, fmt.Errorf("reading the snapshot: %w", err)
	}
	if len(row.RefNames) != len(row.RefShas) {
		return git.Snapshot{}, fmt.Errorf("reading the snapshot: %d refs with %d objects", len(row.RefNames), len(row.RefShas))
	}
	if len(row.Packs) != len(row.PackUploads) {
		return git.Snapshot{}, fmt.Errorf("reading the snapshot: %d packs, %d uploads", len(row.Packs), len(row.PackUploads))
	}
	snapshot := git.Snapshot{}
	for i, name := range row.Packs {
		snapshot.Packs = append(snapshot.Packs, git.Pack{Name: name, Upload: row.PackUploads[i]})
	}
	for i, name := range row.RefNames {
		snapshot.Refs = append(snapshot.Refs, git.Ref{Name: name, SHA: row.RefShas[i]})
	}
	return snapshot, nil
}

// Record applies the push in one transaction that acts for its pusher (git.Store).
func (s *GitStore) Record(ctx context.Context, push git.Push) error {
	return transact(ctx, s.pool, s.q, "recording the push", push.PusherID, func(ctx context.Context, q *queries.Queries) error {
		orgID, err := q.LockRepository(ctx, push.RepoID)
		if errors.Is(err, pgx.ErrNoRows) {
			return git.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("locking the repository: %w", err)
		}
		recorded, err := q.PushRecorded(ctx, push.ID)
		if err != nil {
			return fmt.Errorf("looking the push up: %w", err)
		}
		if recorded {
			return nil
		}
		return s.apply(ctx, q, orgID, push)
	})
}

// apply records a push no transaction recorded before: once its refs prove to be where it found them, it locks the
// repository's branches, stores the push, moves its refs and stores the branches' facts, then tells the push workers.
func (s *GitStore) apply(ctx context.Context, q *queries.Queries, orgID uuid.UUID, push git.Push) error {
	if err := checkRefs(ctx, q, push); err != nil {
		return err
	}
	if err := q.LockLiveBranches(ctx, push.RepoID); err != nil {
		return fmt.Errorf("locking the branches: %w", err)
	}
	if err := s.insertPush(ctx, q, orgID, push); err != nil {
		return err
	}
	if err := moveRefs(ctx, q, orgID, push); err != nil {
		return err
	}
	if err := recordFacts(ctx, q, orgID, push); err != nil {
		return err
	}
	if err := orgEvent(ctx, q, &orgID, push.PusherID, "repo.pushed", push.RepoID, map[string]any{
		"push": push.ID, "refs": updatesJSON(push.Updates), "commits": len(push.Commits),
	}); err != nil {
		return err
	}
	if err := q.NotifyPush(ctx, push.ID.String()); err != nil {
		return fmt.Errorf("notifying the push workers: %w", err)
	}
	return nil
}

// checkRefs returns a *git.StaleError for the first ref the push would move from a value it no longer has.
func checkRefs(ctx context.Context, q *queries.Queries, push git.Push) error {
	rows, err := q.RefsOf(ctx, push.RepoID)
	if err != nil {
		return fmt.Errorf("reading the refs: %w", err)
	}
	current := map[string]string{}
	for _, r := range rows {
		current[r.Name] = r.Sha
	}
	for _, u := range push.Updates {
		now, ok := current[u.Name]
		if !ok {
			now = git.ZeroSHA
		}
		if now != u.Old {
			return &git.StaleError{Ref: u.Name}
		}
	}
	return nil
}

// insertPush lists the push's pack and records the push and its commits.
func (s *GitStore) insertPush(ctx context.Context, q *queries.Queries, orgID uuid.UUID, push git.Push) error {
	pack := ""
	if push.Pack != nil {
		pack = push.Pack.Name
		if err := q.InsertPack(ctx, queries.InsertPackParams{RepoID: push.RepoID, Name: pack, OrgID: orgID, Upload: push.Pack.Upload,
			Size: push.Pack.Size, Objects: push.Pack.Objects}); err != nil {
			return fmt.Errorf("listing the pack: %w", err)
		}
	}
	updates, err := json.Marshal(updatesJSON(push.Updates))
	if err != nil {
		return fmt.Errorf("encoding the ref updates: %w", err)
	}
	if err := q.InsertPush(ctx, queries.InsertPushParams{
		ID: push.ID, OrgID: orgID, RepoID: push.RepoID, PusherID: &push.PusherID, Transport: string(push.Transport),
		RefUpdates: updates, Pack: pack, ReceivedAt: push.ReceivedAt, Traceparent: traceparent(ctx),
	}); err != nil {
		return fmt.Errorf("recording the push: %w", err)
	}
	return insertCommits(ctx, q, orgID, push)
}

// insertCommits records the push's commits in one batch; the push worker resolves their authors and committers.
func insertCommits(ctx context.Context, q *queries.Queries, orgID uuid.UUID, push git.Push) error {
	if len(push.Commits) == 0 {
		return nil
	}
	rows := make([]queries.InsertCommitParams, 0, len(push.Commits))
	for _, c := range push.Commits {
		rows = append(rows, queries.InsertCommitParams{
			RepoID: push.RepoID, Sha: c.SHA, OrgID: orgID, Tree: c.Tree, Parents: nonNil(c.Parents), Message: c.Message,
			AuthorName: c.Author.Name, AuthorEmail: c.Author.Email, AuthoredAt: c.Author.At, AuthorZone: c.Author.Zone,
			CommitterName: c.Committer.Name, CommitterEmail: c.Committer.Email, CommittedAt: c.Committer.At,
			CommitterZone: c.Committer.Zone, Signature: c.Signature, FirstPush: push.ID,
		})
	}
	var failed error
	q.InsertCommit(ctx, rows).Exec(func(i int, err error) {
		if err != nil && failed == nil {
			failed = fmt.Errorf("recording commit %s: %w", rows[i].Sha, err)
		}
	})
	return failed
}

// Process claims the push that has waited longest and processes it in one transaction (git.Store). A push it fails
// is kept waiting in a transaction of its own, the failed one rolled back.
func (s *GitStore) Process(ctx context.Context, retry func(attempts int) time.Duration) (uuid.UUID, error) {
	var claimed queries.ClaimPushRow
	err := transact(ctx, s.pool, s.q, "processing a push", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		var err error
		claimed, err = q.ClaimPush(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("claiming a push: %w", err)
		}
		linkPush(ctx, claimed.Traceparent)
		if err := s.attribute(ctx, q, claimed); err != nil {
			return err
		}
		if err := restatusWaiting(ctx, q, claimed.RepoID, claimed.ID); err != nil {
			return err
		}
		if err := q.MarkPushProcessed(ctx, claimed.ID); err != nil {
			return fmt.Errorf("marking the push processed: %w", err)
		}
		return nil
	})
	if err == nil || claimed.ID == uuid.Nil {
		return claimed.ID, err
	}
	delay := retry(int(claimed.Attempts) + 1)
	if failed := s.q.FailPush(ctx, err.Error(), delay.Seconds(), claimed.ID); failed != nil {
		return claimed.ID, errors.Join(err, fmt.Errorf("keeping the push for another try: %w", failed))
	}
	return claimed.ID, err
}

// traceparent is the W3C traceparent of the span in ctx, or "" without one.
func traceparent(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return carrier.Get("traceparent")
}

// linkPush links the span in ctx to the trace that recorded a push, which its traceparent names, so the push worker's
// trace leads back to the push's.
func linkPush(ctx context.Context, traceparent string) {
	if traceparent == "" {
		return
	}
	pushed := propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{"traceparent": traceparent})
	trace.SpanFromContext(ctx).AddLink(trace.LinkFromContext(pushed))
}

// attribute resolves the authors and committers of the commits the claimed push first brought to their principals.
func (s *GitStore) attribute(ctx context.Context, q *queries.Queries, claimed queries.ClaimPushRow) error {
	commits, err := q.PushCommits(ctx, claimed.ID)
	if err != nil {
		return fmt.Errorf("reading the push's commits: %w", err)
	}
	if len(commits) == 0 {
		return nil
	}
	principals := map[string]*uuid.UUID{}
	rows := make([]queries.SetCommitPrincipalsParams, 0, len(commits))
	for _, c := range commits {
		author, err := s.principalOf(ctx, q, principals, c.AuthorName, c.AuthorEmail, claimed.ReceivedAt)
		if err != nil {
			return err
		}
		committer, err := s.principalOf(ctx, q, principals, c.CommitterName, c.CommitterEmail, claimed.ReceivedAt)
		if err != nil {
			return err
		}
		rows = append(rows, queries.SetCommitPrincipalsParams{AuthorID: author, CommitterID: committer, RepoID: claimed.RepoID, Sha: c.Sha})
	}
	var failed error
	q.SetCommitPrincipals(ctx, rows).Exec(func(i int, err error) {
		if err != nil && failed == nil {
			failed = fmt.Errorf("attributing commit %s: %w", rows[i].Sha, err)
		}
	})
	return failed
}

// principalOf resolves who a commit's author or committer is, by lowercased email: the user who holds it verified, or
// else the login-less user who holds it, or else a new login-less user holding it unverified, named after the commit.
// An empty email, or one a user holds unverified, is nobody. resolved remembers each email's answer for the push.
func (s *GitStore) principalOf(ctx context.Context, q *queries.Queries, resolved map[string]*uuid.UUID, name, email string, at time.Time) (*uuid.UUID, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, nil
	}
	if id, ok := resolved[email]; ok {
		return id, nil
	}
	id, err := holder(ctx, q, email)
	if errors.Is(err, pgx.ErrNoRows) {
		id, err = s.loginless(ctx, q, email, name, at)
	}
	if err != nil {
		return nil, err
	}
	resolved[email] = id
	return id, nil
}

// holder returns who holds email, as principalOf counts them, or pgx.ErrNoRows when nobody does.
func holder(ctx context.Context, q *queries.Queries, email string) (*uuid.UUID, error) {
	row, err := q.EmailPrincipal(ctx, email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pgx.ErrNoRows
		}
		return nil, fmt.Errorf("finding who holds %s: %w", email, err)
	}
	if !row.Verified && row.SignsIn {
		return nil, nil
	}
	return &row.PrincipalID, nil
}

// loginless makes a login-less user holding email unverified, named name, in a savepoint: when another push made one
// for the same email first, it rolls back and returns that one.
func (s *GitStore) loginless(ctx context.Context, q *queries.Queries, email, name string, at time.Time) (*uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("making an ID: %w", err)
	}
	err = transact(ctx, s.pool, s.q, "making a login-less user", uuid.Nil, func(ctx context.Context, q *queries.Queries) error {
		if err := q.InsertPrincipal(ctx, queries.InsertPrincipalParams{ID: id, Kind: "user", DisplayName: loginlessName(name, email), CreatedAt: at}); err != nil {
			return fmt.Errorf("inserting the user: %w", err)
		}
		if err := q.InsertEmail(ctx, email, id, false); err != nil {
			return fmt.Errorf("inserting the email: %w", err)
		}
		return nil
	})
	var conflict *ConflictError
	if errors.As(err, &conflict) {
		return holder(ctx, q, email)
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// loginlessName is a login-less user's name: the commit's, or the part of the email before @, at most 100 characters.
func loginlessName(name, email string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name, _, _ = strings.Cut(email, "@")
	}
	if utf8.RuneCountInString(name) > 100 {
		name = string([]rune(name)[:100])
	}
	return name
}

// moveRefs moves the push's refs and the branches they are.
func moveRefs(ctx context.Context, q *queries.Queries, orgID uuid.UUID, push git.Push) error {
	at := time.Now()
	for _, u := range push.Updates {
		if u.New == git.ZeroSHA {
			if err := q.DeleteRef(ctx, push.RepoID, u.Name); err != nil {
				return fmt.Errorf("deleting %s: %w", u.Name, err)
			}
		} else if err := q.SetRef(ctx, queries.SetRefParams{RepoID: push.RepoID, Name: u.Name, OrgID: orgID, Sha: u.New}); err != nil {
			return fmt.Errorf("moving %s: %w", u.Name, err)
		}
		branch, ok := git.Branch(u.Name)
		if !ok {
			continue
		}
		if err := moveBranch(ctx, q, orgID, push.RepoID, branch, u, at); err != nil {
			return err
		}
	}
	return nil
}

// moveBranch creates, moves or deletes the branch u updates.
func moveBranch(ctx context.Context, q *queries.Queries, orgID, repoID uuid.UUID, name string, u git.RefUpdate, at time.Time) error {
	switch {
	case u.Old == git.ZeroSHA:
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("making an ID: %w", err)
		}
		if err := q.InsertBranch(ctx, queries.InsertBranchParams{ID: id, OrgID: orgID, RepoID: repoID, Name: name, Head: u.New, At: at}); err != nil {
			return fmt.Errorf("creating branch %s: %w", name, err)
		}
	case u.New == git.ZeroSHA:
		if _, err := q.DeleteBranch(ctx, at, repoID, name); err != nil {
			return fmt.Errorf("deleting branch %s: %w", name, err)
		}
	default:
		if _, err := q.MoveBranch(ctx, queries.MoveBranchParams{Head: u.New, At: at, RepoID: repoID, Name: name}); err != nil {
			return fmt.Errorf("moving branch %s: %w", name, err)
		}
	}
	return nil
}

// refUpdateJSON is how a push's row and its event write a ref it moved.
type refUpdateJSON struct {
	Name string `json:"name"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

func updatesJSON(updates []git.RefUpdate) []refUpdateJSON {
	out := make([]refUpdateJSON, 0, len(updates))
	for _, u := range updates {
		out = append(out, refUpdateJSON(u))
	}
	return out
}

// nonNil is s, or an empty slice for a nil one: a root commit's parents are none, not unknown.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
