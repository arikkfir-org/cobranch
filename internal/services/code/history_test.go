package code_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/repos"
)

// entry is the n-th commit as a walk of history names it, with the parents' numbers.
func entry(n int, parents ...int) code.LogEntry {
	e := code.LogEntry{SHA: sha(n), Parents: []string{}}
	for _, p := range parents {
		e.Parents = append(e.Parents, sha(p))
	}
	return e
}

// withLogs gives the fixture's Git server its walks of history: from main's merge 5, from 6, from v1's 4, and from
// fresh's 8, which merges nothing new on top of 5 and which no push recorded yet.
func withLogs(f *fixture) *fixture {
	tail := []code.LogEntry{entry(4, 3), entry(3, 2), entry(2, 1), entry(1)}
	f.objects.History(sha(5), append([]code.LogEntry{entry(5, 4, 6), entry(6, 2)}, tail...), "main")
	f.objects.History(sha(6), []code.LogEntry{entry(6, 2), entry(2, 1), entry(1)}, "feature/x")
	f.objects.History(sha(4), tail, "v1")
	f.objects.History(sha(8), append([]code.LogEntry{entry(8, 5), entry(5, 4, 6), entry(6, 2)}, tail...), "fresh")
	f.objects.PathHistory(sha(5), "docs", []code.LogEntry{entry(6, 2), entry(2), entry(77)})
	for _, n := range []int{1, 2, 3, 4, 5, 6, 8, 77} {
		f.objects.Messages[sha(n)] = fmt.Sprintf("Commit %d\n", n)
	}
	return f
}

// historyCases are TestHistory's: what a query lists, where its next page starts, and what it fails with.
var historyCases = []struct {
	name     string
	nia      bool
	q        code.HistoryQuery
	want     []string
	wantNext string
	wantErr  error
}{
	{name: "the first parents, nearest first", q: code.HistoryQuery{Branch: "main"}, want: []string{"5", "4", "3", "2", "1"}},
	{name: "a page and where the next starts", q: code.HistoryQuery{Branch: "main", Limit: 2}, want: []string{"5", "4"}, wantNext: sha(3)},
	{name: "the next page", q: code.HistoryQuery{Branch: "main", Cursor: sha(3), Limit: 2}, want: []string{"3", "2"}, wantNext: sha(1)},
	{name: "the last page", q: code.HistoryQuery{Branch: "main", Cursor: sha(1), Limit: 2}, want: []string{"1"}},
	{name: "a page as long as what's left", q: code.HistoryQuery{Branch: "main", Limit: 5}, want: []string{"5", "4", "3", "2", "1"}},
	{name: "another branch", q: code.HistoryQuery{Branch: "feature/x"}, want: []string{"6", "2", "1"}},
	{name: "a branch at no recorded commit has no history", q: code.HistoryQuery{Branch: "stray"}},
	{name: "a branch that doesn't live", q: code.HistoryQuery{Branch: "gone"}, wantErr: repos.ErrNotFound},
	{name: "first parents from a ref that is a branch", q: code.HistoryQuery{Branch: "main", Ref: "feature/x"}, want: []string{"6", "2", "1"}},
	{name: "first parents from a tag, which the Git server finds", q: code.HistoryQuery{Ref: "v1", Parents: code.ParentsFirst}, want: []string{"4", "3", "2", "1"}},
	{name: "first parents from a commit's full name", q: code.HistoryQuery{Ref: sha(6)}, want: []string{"6", "2", "1"}},
	{name: "first parents from a ref at no recorded commit", q: code.HistoryQuery{Ref: "stray"}},
	{name: "first parents from a commit no push recorded", q: code.HistoryQuery{Ref: sha(77)}, wantErr: code.ErrRefNotFound},
	{name: "first parents from a ref that names nothing", q: code.HistoryQuery{Ref: "gone"}, wantErr: code.ErrRefNotFound},
	{name: "every parent, newest first", q: code.HistoryQuery{Branch: "main", Parents: code.ParentsAll}, want: []string{"5", "6", "4", "3", "2", "1"}},
	{name: "every parent from a tag", q: code.HistoryQuery{Branch: "main", Ref: "v1", Parents: code.ParentsAll}, want: []string{"4", "3", "2", "1"}},
	{name: "every parent from a commit's full name", q: code.HistoryQuery{Ref: sha(6), Parents: code.ParentsAll}, want: []string{"6", "2", "1"}},
	{name: "every parent, without what no push recorded yet", q: code.HistoryQuery{Ref: "fresh", Parents: code.ParentsAll}, want: []string{"5", "6", "4", "3", "2", "1"}},
	{name: "every parent from a ref that names nothing", q: code.HistoryQuery{Ref: "gone", Parents: code.ParentsAll}, wantErr: code.ErrRefNotFound},
	{name: "every parent from a ref git would read as a revision", q: code.HistoryQuery{Ref: "main~2", Parents: code.ParentsAll}, wantErr: invalid("ref.invalid")},
	{name: "every parent from a first parent's cursor", q: code.HistoryQuery{Branch: "main", Parents: code.ParentsAll, Cursor: sha(3)}, wantErr: invalid("cursor.invalid")},
	{name: "every parent from a cursor of nothing", q: code.HistoryQuery{Branch: "main", Parents: code.ParentsAll, Cursor: "nonsense"}, wantErr: invalid("cursor.invalid")},
	{name: "parents that are neither", q: code.HistoryQuery{Branch: "main", Parents: "some"}, wantErr: invalid("parents.invalid")},
	{name: "a path's, every parent, without what no push recorded", q: code.HistoryQuery{Branch: "main", Path: "docs"}, want: []string{"6", "2"}},
	{name: "a path's from a ref, every parent asked", q: code.HistoryQuery{Ref: "main", Path: "docs", Parents: code.ParentsAll}, want: []string{"6", "2"}},
	{name: "a path's page and where the next starts", q: code.HistoryQuery{Branch: "main", Path: "docs", Limit: 1}, want: []string{"6"},
		wantNext: "MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwNTox"},
	{name: "a path nothing changed", q: code.HistoryQuery{Branch: "main", Path: "src"}},
	{name: "a path's along first parents", q: code.HistoryQuery{Branch: "main", Path: "docs", Parents: code.ParentsFirst}, wantErr: invalid("parents.path")},
	{name: "a path's with parents that are neither", q: code.HistoryQuery{Branch: "main", Path: "docs", Parents: "some"}, wantErr: invalid("parents.invalid")},
	{name: "a path that climbs out", q: code.HistoryQuery{Branch: "main", Path: "docs/../.."}, wantErr: invalid("path.invalid")},
	{name: "a cursor that names no commit", q: code.HistoryQuery{Branch: "main", Cursor: sha(77)}, wantErr: invalid("cursor.invalid")},
	{name: "a cursor that is no object name", q: code.HistoryQuery{Branch: "main", Cursor: "HEAD~2"}, wantErr: invalid("cursor.invalid")},
	{name: "a page too long", q: code.HistoryQuery{Branch: "main", Limit: 101}, wantErr: invalid("limit.invalid")},
	{name: "a negative page", q: code.HistoryQuery{Branch: "main", Limit: -1}, wantErr: invalid("limit.invalid")},
	{name: "a viewer who doesn't read the repository", nia: true, q: code.HistoryQuery{Branch: "main", Parents: code.ParentsAll}, wantErr: repos.ErrNotFound},
}

func TestHistory(t *testing.T) {
	for _, tt := range historyCases {
		t.Run(tt.name, func(t *testing.T) {
			f := withLogs(newFixture())
			v := f.max
			if tt.nia {
				v = f.nia
			}
			got, err := f.service.History(t.Context(), v, f.repo, tt.q)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			if !slices.Equal(shas(got.Commits), tt.want) || got.Next != tt.wantNext {
				t.Errorf("got %v next %q, want %v next %q", shas(got.Commits), got.Next, tt.want, tt.wantNext)
			}
		})
	}
}

// TestHistoryEveryParentPages follows every parent's pages to the end, and finds them holding still after the branch
// moved on.
func TestHistoryEveryParentPages(t *testing.T) {
	f := withLogs(newFixture())
	var got []string
	q := code.HistoryQuery{Branch: "main", Parents: code.ParentsAll, Limit: 4}
	for pages := 0; pages < 10; pages++ {
		page, err := f.service.History(t.Context(), f.max, f.repo, q)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, shas(page.Commits)...)
		if page.Next == "" {
			break
		}
		f.objects.Refs["main"] = sha(8) // the branch moves on; the next page keeps to where the first started
		q.Cursor = page.Next
	}
	if want := []string{"5", "6", "4", "3", "2", "1"}; !slices.Equal(got, want) {
		t.Errorf("got %v over the pages, want %v", got, want)
	}
}

func TestHistoryWithoutGitServer(t *testing.T) {
	f := newFixture()
	service := code.NewService(f.store, nil, f.markdown, f.authz)
	if _, err := service.History(t.Context(), f.max, f.repo, code.HistoryQuery{Branch: "main", Parents: code.ParentsAll}); !sameError(err, code.ErrUnavailable) {
		t.Errorf("every parent without a Git server: %v, want %v", err, code.ErrUnavailable)
	}
	if got, err := service.History(t.Context(), f.max, f.repo, code.HistoryQuery{Branch: "main"}); err != nil || len(got.Commits) != 5 {
		t.Errorf("first parents without a Git server = %v, %v, want main's five", shas(got.Commits), err)
	}
}

func TestHistoryDefaultPage(t *testing.T) {
	f := newFixture()
	f.store.Add(f.repo, f.push, code.Commit{SHA: sha(7), Parents: []string{sha(5)}})
	for n := 8; n < 7+code.DefaultPageSize+5; n++ {
		f.store.Add(f.repo, f.push, code.Commit{SHA: sha(n), Parents: []string{sha(n - 1)}})
	}
	f.store.SetBranch(f.repo, "main", sha(6+code.DefaultPageSize+5))
	got, err := f.service.History(t.Context(), f.max, f.repo, code.HistoryQuery{Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Commits) != code.DefaultPageSize || got.Next == "" {
		t.Errorf("got %d commits and next %q, want %d and a next page", len(got.Commits), got.Next, code.DefaultPageSize)
	}
}

// TestHistoryQueryAndTotal follows a query through both walks, and the count each first page carries.
func TestHistoryQueryAndTotal(t *testing.T) {
	none := 0
	total := func(n int) *int { return &n }
	tests := []struct {
		name      string
		q         code.HistoryQuery
		want      []string
		wantNext  string
		wantTotal *int
		wantErr   error
	}{
		{name: "first parents, counted", q: code.HistoryQuery{Branch: "main", Limit: 2}, want: []string{"5", "4"}, wantNext: sha(3), wantTotal: total(5)},
		{name: "first parents' later page, uncounted", q: code.HistoryQuery{Branch: "main", Cursor: sha(3), Limit: 2}, want: []string{"3", "2"}, wantNext: sha(1)},
		{name: "first parents whose message holds a query, ignoring case", q: code.HistoryQuery{Branch: "main", Query: "COMMIT 3"}, want: []string{"3"},
			wantTotal: total(1)},
		{name: "a query's page and where its next starts", q: code.HistoryQuery{Branch: "main", Query: "commit", Limit: 2}, want: []string{"5", "4"},
			wantNext: sha(3), wantTotal: total(5)},
		{name: "a query's next page", q: code.HistoryQuery{Branch: "main", Query: "commit 1", Cursor: sha(3)}, want: []string{"1"}},
		{name: "a query nothing holds", q: code.HistoryQuery{Branch: "main", Query: "nothing like it"}, wantTotal: &none},
		{name: "a query from a commit no push recorded", q: code.HistoryQuery{Ref: sha(77), Query: "commit"}, wantErr: code.ErrRefNotFound},
		{name: "a query's cursor of nothing", q: code.HistoryQuery{Branch: "main", Query: "commit", Cursor: sha(77)}, wantErr: invalid("cursor.invalid")},
		{name: "a branch at no recorded commit counts none", q: code.HistoryQuery{Branch: "stray"}, wantTotal: &none},
		{name: "every parent, counted", q: code.HistoryQuery{Branch: "main", Parents: code.ParentsAll}, want: []string{"5", "6", "4", "3", "2", "1"},
			wantTotal: total(6)},
		{name: "every parent whose message holds a query", q: code.HistoryQuery{Branch: "main", Parents: code.ParentsAll, Query: "commit 6"},
			want: []string{"6"}, wantTotal: total(1)},
		{name: "every parent's query, a page and where its next starts", q: code.HistoryQuery{Branch: "main", Parents: code.ParentsAll, Query: "commit", Limit: 2},
			want: []string{"5", "6"}, wantNext: "MDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwNToy", wantTotal: total(6)},
		{name: "a path's, counted as git counts it", q: code.HistoryQuery{Branch: "main", Path: "docs"}, want: []string{"6", "2"}, wantTotal: total(3)},
		{name: "a path's whose message holds a query", q: code.HistoryQuery{Branch: "main", Path: "docs", Query: "commit 2"}, want: []string{"2"},
			wantTotal: total(1)},
		{name: "a query too long", q: code.HistoryQuery{Branch: "main", Query: strings.Repeat("a", code.MaxQuery+1)}, wantErr: invalid("q.invalid")},
		{name: "a query of the longest", q: code.HistoryQuery{Branch: "main", Query: strings.Repeat("ä", code.MaxQuery)}, wantTotal: &none},
		{name: "a query over two lines", q: code.HistoryQuery{Branch: "main", Query: "commit\n3"}, wantErr: invalid("q.invalid")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := withLogs(newFixture())
			got, err := f.service.History(t.Context(), f.max, f.repo, tt.q)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			if !slices.Equal(shas(got.Commits), tt.want) || got.Next != tt.wantNext {
				t.Errorf("got %v next %q, want %v next %q", shas(got.Commits), got.Next, tt.want, tt.wantNext)
			}
			if (got.Total == nil) != (tt.wantTotal == nil) || got.Total != nil && *got.Total != *tt.wantTotal {
				t.Errorf("got a total of %v, want %v", deref(got.Total), deref(tt.wantTotal))
			}
		})
	}
}

// deref is what n points at, or "none".
func deref(n *int) any {
	if n == nil {
		return "none"
	}
	return *n
}
