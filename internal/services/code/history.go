package code

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// Parents is which of each commit's parents a history follows.
type Parents string

// The parents a history follows.
const (
	// ParentsFirst follows each commit's first parent, as git log --first-parent does: a branch's line of merges,
	// walked in PostgreSQL, without the Git server.
	ParentsFirst Parents = "first"
	// ParentsAll follows every parent, as git log --date-order does: a merge's other lines too, walked by the Git server.
	ParentsAll Parents = "all"
)

// HistoryQuery is the page of history a caller asks for: from the commit Ref names (a full commit name, a branch or a
// tag, in that order), or else from Branch's head; following Parents (ParentsFirst when empty, ParentsAll when it
// names a Path); only the commits that changed what is at Path, when it names one; only those whose message holds
// Query, ignoring case, when it is set; from Cursor, where the previous page said the next one starts, instead of
// either; and at most Limit commits, 0 asking for DefaultPageSize.
type HistoryQuery struct {
	Branch  string
	Ref     string
	Path    string
	Query   string
	Parents Parents
	Cursor  string
	Limit   int
}

// MaxQuery is the most characters a history's Query may hold.
const MaxQuery = 200

// History lists a page of the repository's history, nearest first; it takes repo.read. Following first parents, it
// walks the commits pushes recorded, and a branch whose head is no commit a push recorded has none. Following every
// parent, the Git server walks the snapshot in git log --date-order (no commit before its children), and the page leaves out any commit
// no push recorded; its cursor holds the commit the first page started from and how many came
// before it, so later pages hold still while the branch moves on. A path's history follows every parent, and holds
// the commits that changed what is there, as git log -- path lists them. A query keeps the commits whose message holds
// it, as git log -i -F --grep does. The first page counts the commits of the whole history, as git rev-list --count
// does, so a caller can say how many there are without walking them all.
func (s *Service) History(ctx context.Context, v Viewer, repoID uuid.UUID, q HistoryQuery) (Page, error) {
	ctx, span := tracer.Start(ctx, "code.Service.History")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.branch", q.Branch),
		attribute.String("cobranch.ref", q.Ref), attribute.String("cobranch.path", q.Path), attribute.String("cobranch.parents", string(q.Parents)),
		attribute.Bool("cobranch.history.query", q.Query != ""))
	if err := s.read(ctx, v, repoID); err != nil {
		return Page{}, failed(span, "reading the history", err)
	}
	size, err := pageSize(q.Limit)
	if err != nil {
		return Page{}, err
	}
	if err := ValidatePath(q.Path); err != nil {
		return Page{}, err
	}
	if err := validateQuery(q.Query); err != nil {
		return Page{}, err
	}
	var page Page
	switch {
	case q.Parents != "" && q.Parents != ParentsFirst && q.Parents != ParentsAll:
		return Page{}, &orgs.ValidationError{Field: "parents", Code: "parents.invalid", Message: "first or all"}
	case q.Path != "" && q.Parents == ParentsFirst:
		return Page{}, &orgs.ValidationError{Field: "parents", Code: "parents.path",
			Message: "a path's history follows every parent, as git log -- path does"}
	case q.Path != "":
		page, err = s.everyParent(ctx, repoID, q, size)
	default:
		page, err = s.byParents(ctx, repoID, q, size)
	}
	if err == nil {
		err = s.renderCommits(ctx, repoID, page.Commits)
	}
	if err != nil {
		return Page{}, failed(span, "reading the history", err)
	}
	return page, nil
}

// byParents is a page of history of the whole repository, following the parents q asks for.
func (s *Service) byParents(ctx context.Context, repoID uuid.UUID, q HistoryQuery, size int) (Page, error) {
	if q.Parents == ParentsAll {
		return s.everyParent(ctx, repoID, q, size)
	}
	return s.firstParents(ctx, repoID, q, size)
}

// firstParents is a page of history along first parents, from the commit pushes recorded where it starts, counted
// on the first page.
func (s *Service) firstParents(ctx context.Context, repoID uuid.UUID, q HistoryQuery, size int) (Page, error) {
	from, err := s.firstFrom(ctx, repoID, q)
	if err != nil || from == "" {
		return Page{Total: counted(q, 0)}, err
	}
	commits, err := s.store.FirstParents(ctx, repoID, from, q.Query, size+1)
	if err != nil {
		return Page{}, fmt.Errorf("walking first parents from %s: %w", from, err)
	}
	if len(commits) == 0 {
		if err := s.walked(ctx, repoID, q, from); err != nil {
			return Page{}, err
		}
	}
	page := Page{Commits: commits}
	if len(commits) > size {
		page.Commits, page.Next = commits[:size], commits[size].SHA
	}
	if q.Cursor == "" {
		n, err := s.store.CountFirstParents(ctx, repoID, from, q.Query)
		if err != nil {
			return Page{}, fmt.Errorf("counting first parents from %s: %w", from, err)
		}
		page.Total = &n
	}
	return page, nil
}

// walked tells a walk along first parents that a query left empty from one that never started, because the commit
// it starts from is none a push recorded: a cursor of nothing answers cursor.invalid, and any other start
// ErrRefNotFound.
func (s *Service) walked(ctx context.Context, repoID uuid.UUID, q HistoryQuery, from string) error {
	if q.Query != "" {
		recorded, err := s.store.CommitsAt(ctx, repoID, []string{from})
		if err != nil {
			return fmt.Errorf("finding commit %s: %w", from, err)
		}
		if _, ok := recorded[from]; ok {
			return nil
		}
	}
	if q.Cursor != "" {
		return &orgs.ValidationError{Field: "cursor", Code: "cursor.invalid", Message: "no page of this repository's history starts here"}
	}
	return fmt.Errorf("finding commit %s: %w", from, ErrRefNotFound)
}

// counted is the count a first page carries when its history holds n commits, and nothing on a later page.
func counted(q HistoryQuery, n int) *int {
	if q.Cursor != "" {
		return nil
	}
	return &n
}

// validateQuery answers q.invalid for a query longer than MaxQuery or holding a control character, such as a line
// break, which no message search needs.
func validateQuery(query string) error {
	if utf8.RuneCountInString(query) <= MaxQuery && !strings.ContainsFunc(query, unicode.IsControl) && utf8.ValidString(query) {
		return nil
	}
	return &orgs.ValidationError{Field: "q", Code: "q.invalid", Message: "up to 200 characters, on one line"}
}

// firstFrom is where a page of history along first parents starts: the cursor when the caller gave one; the commit
// Ref names, which only a tag needs the Git server to find; or Branch's head. It is nothing for a branch whose head is
// no commit a push recorded.
func (s *Service) firstFrom(ctx context.Context, repoID uuid.UUID, q HistoryQuery) (string, error) {
	switch {
	case q.Cursor != "":
		return q.Cursor, ValidateSHA("cursor", q.Cursor)
	case q.Ref == "":
		b, err := s.store.Branch(ctx, repoID, q.Branch)
		if err != nil {
			return "", fmt.Errorf("finding branch %q: %w", q.Branch, err)
		}
		if b.Commit == nil {
			return "", nil
		}
		return b.Head, nil
	}
	if err := ValidateRef(q.Ref); err != nil || shaPattern.MatchString(q.Ref) {
		return q.Ref, err
	}
	b, err := s.store.Branch(ctx, repoID, q.Ref)
	switch {
	case err == nil && b.Commit == nil:
		return "", nil
	case err == nil:
		return b.Head, nil
	case !errors.Is(err, repos.ErrNotFound):
		return "", fmt.Errorf("finding branch %q: %w", q.Ref, err)
	}
	log, err := s.objects.Log(ctx, repoID, LogQuery{Ref: q.Ref})
	if err != nil {
		return "", fmt.Errorf("finding the commit of %q: %w", q.Ref, err)
	}
	return log.Commit, nil
}

// everyParent is a page of history along every parent, as the Git server walks and counts it, with the commits pushes
// recorded: of the whole repository, or of what is at q.Path when it names one.
func (s *Service) everyParent(ctx context.Context, repoID uuid.UUID, q HistoryQuery, size int) (Page, error) {
	ref, skip := q.Ref, 0
	if ref == "" {
		ref = q.Branch
	}
	var err error
	if q.Cursor != "" {
		ref, skip, err = decodeCursor(q.Cursor)
	} else {
		err = ValidateRef(ref)
	}
	if err != nil {
		return Page{}, err
	}
	log, err := s.objects.Log(ctx, repoID, LogQuery{Ref: ref, Path: q.Path, Grep: q.Query, Skip: skip, Limit: size + 1, Count: q.Cursor == ""})
	switch {
	case err != nil:
		return Page{}, fmt.Errorf("walking the history of %q: %w", ref, err)
	case len(log.Entries) == 0 && q.Cursor != "":
		return Page{}, &orgs.ValidationError{Field: "cursor", Code: "cursor.invalid", Message: "no page of this repository's history starts here"}
	}
	page := Page{Total: counted(q, log.Total)}
	if len(log.Entries) > size {
		log.Entries, page.Next = log.Entries[:size], encodeCursor(log.Commit, skip+size)
	}
	shas := make([]string, 0, len(log.Entries))
	for _, e := range log.Entries {
		shas = append(shas, e.SHA)
	}
	recorded, err := s.store.CommitsAt(ctx, repoID, shas)
	if err != nil {
		return Page{}, fmt.Errorf("reading the commits of the history of %q: %w", ref, err)
	}
	for _, sha := range shas {
		if c, ok := recorded[sha]; ok {
			page.Commits = append(page.Commits, c)
		}
	}
	return page, nil
}

// encodeCursor is the cursor of a page of history along every parent: the commit the first page started from and how
// many commits come before the page, opaque to callers.
func encodeCursor(commit string, skip int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(commit + ":" + strconv.Itoa(skip)))
}

// decodeCursor reads a cursor encodeCursor made, or answers cursor.invalid.
func decodeCursor(cursor string) (string, int, error) {
	invalid := &orgs.ValidationError{Field: "cursor", Code: "cursor.invalid", Message: "no page of this repository's history starts here"}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", 0, invalid
	}
	commit, n, ok := strings.Cut(string(raw), ":")
	skip, err := strconv.Atoi(n)
	if !ok || err != nil || skip < 1 || !shaPattern.MatchString(commit) {
		return "", 0, invalid
	}
	return commit, skip, nil
}
