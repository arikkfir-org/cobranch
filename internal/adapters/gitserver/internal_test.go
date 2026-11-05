package gitserver_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"cobranch.dev/internal/adapters/gitinternal"
	"cobranch.dev/internal/adapters/postgres"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"github.com/google/uuid"
)

// browsed is a repository pushed to a Git server, with the commits and objects the internal call's tests read: main's
// first commit, tagged v1 with an annotated tag, holds directories, an executable, a binary, a file larger than the
// API's text, a symlink, a submodule and names that Git's pathspecs would read as patterns; main's head merges branch
// feature into it.
type browsed struct {
	stack    *stack
	client   *gitinternal.Client
	internal string
	// reader is a personal token of Gwen's, who reads the website.
	reader           string
	dir              string
	first, merge     string
	feature, logoSHA string
}

func newBrowsed(t *testing.T) *browsed {
	t.Helper()
	s := newStack(t)
	srv := s.server(t.TempDir())
	smart := httptest.NewServer(srv.Handler())
	t.Cleanup(smart.Close)
	internal := httptest.NewServer(srv.InternalHandler())
	t.Cleanup(internal.Close)
	base, err := url.Parse(internal.URL)
	if err != nil {
		t.Fatal(err)
	}
	b := &browsed{stack: s, client: gitinternal.NewClient(base), internal: internal.URL, reader: s.token(gwen),
		dir: clone(t, s.websiteURL(smart.URL, olivia))}
	files := map[string]string{"README.md": "# Website\n", "docs/guide.md": "Read me.\n", "docs/with space ü.md": "Spaces\n",
		"docs/*.md": "A star\n", "logo.png": "\x89PNG\r\n\x1a\n\x00\x01", "big.txt": strings.Repeat("x", code.MaxText+10), "bin/tool": "#!/bin/sh\n"}
	for name, content := range files {
		write(t, b.dir, name, content)
	}
	if err := os.Chmod(filepath.Join(b.dir, "bin/tool"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("docs/guide.md", filepath.Join(b.dir, "link")); err != nil {
		t.Fatal(err)
	}
	mustGit(t, b.dir, "add", "-A")
	mustGit(t, b.dir, "update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("ab", 20)+",vendor/lib")
	// The first commit's clock ran years ahead of its children's: history still shows it beneath them.
	if out, err := gitCmdAt(t, b.dir, "2031-01-01T00:00:00Z", "commit", "-q", "-m", "First"); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	b.first = strings.TrimSpace(mustGit(t, b.dir, "rev-parse", "HEAD"))
	b.logoSHA = strings.TrimSpace(mustGit(t, b.dir, "rev-parse", "HEAD:logo.png"))
	mustGit(t, b.dir, "tag", "-a", "v1", "-m", "Version 1")
	mustGit(t, b.dir, "checkout", "-q", "-b", "feature")
	b.feature = commit(t, b.dir, "feature.txt", "A feature\n")
	mustGit(t, b.dir, "checkout", "-q", "-")
	mustGit(t, b.dir, "merge", "-q", "--no-ff", "-m", "Merge feature", "feature")
	b.merge = strings.TrimSpace(mustGit(t, b.dir, "rev-parse", "HEAD"))
	mustGit(t, b.dir, "push", "-q", "--follow-tags", "origin", "HEAD:refs/heads/main", "feature")
	return b
}

// write writes content into name under dir, making its directories.
func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInternalTree(t *testing.T) {
	t.Parallel()
	b := newBrowsed(t)
	tests := []struct {
		name, ref, path string
		wantCommit      string
		want            []string
		wantErr         error
	}{
		{name: "main's root, in Git's order", ref: "main", wantCommit: "merge",
			want: []string{"README.md blob 100644 10", "big.txt blob 100644 1048586", "bin tree 040000 0", "docs tree 040000 0", "feature.txt blob 100644 10",
				"link symlink 120000 13", "logo.png blob 100644 10", "vendor tree 040000 0"}},
		{name: "a directory, at an annotated tag", ref: "v1", path: "docs", wantCommit: "first",
			want: []string{"*.md blob 100644 7", "guide.md blob 100644 9", "with space ü.md blob 100644 7"}},
		{name: "an executable, at a commit's full name", ref: "first", path: "bin", wantCommit: "first", want: []string{"tool blob 100755 10"}},
		{name: "a submodule", ref: "feature", path: "vendor", wantCommit: "feature", want: []string{"lib submodule 160000 0"}},
		{name: "a ref that names nothing", ref: "gone", wantErr: code.ErrRefNotFound},
		{name: "an object that is no commit", ref: strings.Repeat("ab", 20), wantErr: code.ErrRefNotFound},
		{name: "a file's path", ref: "main", path: "README.md", wantErr: code.ErrNotTree},
		{name: "a symlink's path", ref: "main", path: "link", wantErr: code.ErrNotTree},
		{name: "a submodule's path", ref: "main", path: "vendor/lib", wantErr: code.ErrSubmodule},
		{name: "a path that holds nothing", ref: "main", path: "nothing", wantErr: code.ErrPathNotFound},
		{name: "a path through a file", ref: "main", path: "README.md/x", wantErr: code.ErrPathNotFound},
		{name: "a pattern is a name", ref: "main", path: "do*", wantErr: code.ErrPathNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree, err := b.client.Tree(b.ctx(t), website, b.named(tt.ref), tt.path)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Tree() error = %v, want %v", err, tt.wantErr)
			}
			var got []string
			for _, e := range tree.Entries {
				got = append(got, strings.Join([]string{e.Name, string(e.Kind), e.Mode, strconv.FormatInt(e.Size, 10)}, " "))
				if want := strings.TrimPrefix(tt.path+"/"+e.Name, "/"); e.Path != want {
					t.Errorf("entry %s's path = %q, want %q", e.Name, e.Path, want)
				}
			}
			if !slices.Equal(got, tt.want) || tree.Commit != b.named(tt.wantCommit) {
				t.Errorf("Tree() = %v at %s, want %v at %s", got, tree.Commit, tt.want, tt.wantCommit)
			}
		})
	}
}

// ctx is a test's context, carrying the reader's token for the client to send on.
func (b *browsed) ctx(t *testing.T) context.Context {
	return auth.WithToken(t.Context(), b.reader)
}

// named is the commit a test's name stands for, or the name itself.
func (b *browsed) named(name string) string {
	switch name {
	case "first":
		return b.first
	case "merge":
		return b.merge
	case "feature":
		return b.feature
	}
	return name
}

func TestInternalBlob(t *testing.T) {
	t.Parallel()
	b := newBrowsed(t)
	tests := []struct {
		name, path    string
		limit         int64
		wantKind      code.EntryKind
		wantMode      string
		wantContent   string
		wantTruncated bool
		wantErr       error
	}{
		{name: "a file", path: "docs/guide.md", limit: 100, wantKind: code.KindBlob, wantMode: "100644", wantContent: "Read me.\n"},
		{name: "a name with a space and an umlaut", path: "docs/with space ü.md", limit: 100, wantKind: code.KindBlob, wantMode: "100644", wantContent: "Spaces\n"},
		{name: "a star is a name", path: "docs/*.md", limit: 100, wantKind: code.KindBlob, wantMode: "100644", wantContent: "A star\n"},
		{name: "a symlink's content is its target", path: "link", limit: 100, wantKind: code.KindSymlink, wantMode: "120000", wantContent: "docs/guide.md"},
		{name: "up to the limit", path: "big.txt", limit: 5, wantKind: code.KindBlob, wantMode: "100644", wantContent: "xxxxx", wantTruncated: true},
		{name: "exactly the limit", path: "docs/guide.md", limit: 9, wantKind: code.KindBlob, wantMode: "100644", wantContent: "Read me.\n"},
		{name: "a directory", path: "docs", limit: 100, wantErr: code.ErrNotBlob},
		{name: "the root", path: "", limit: 100, wantErr: code.ErrNotBlob},
		{name: "a submodule", path: "vendor/lib", limit: 100, wantErr: code.ErrSubmodule},
		{name: "nothing", path: "docs/nothing.md", limit: 100, wantErr: code.ErrPathNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blob, err := b.client.Blob(b.ctx(t), website, "main", tt.path, tt.limit)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Blob() error = %v, want %v", err, tt.wantErr)
			}
			if blob.Kind != tt.wantKind || blob.Mode != tt.wantMode || string(blob.Content) != tt.wantContent || blob.Truncated != tt.wantTruncated {
				t.Errorf("Blob() = %s %s %q truncated %v", blob.Kind, blob.Mode, blob.Content, blob.Truncated)
			}
			if err == nil && (blob.Commit != b.merge || blob.Path != tt.path) {
				t.Errorf("Blob() at %s, %q", blob.Commit, blob.Path)
			}
		})
	}
}

func TestInternalRaw(t *testing.T) {
	t.Parallel()
	b := newBrowsed(t)
	raw, err := b.client.Raw(b.ctx(t), website, "v1", "logo.png")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Body.Close() }()
	body, err := io.ReadAll(raw.Body)
	if err != nil || !bytes.Equal(body, []byte("\x89PNG\r\n\x1a\n\x00\x01")) || raw.Size != 10 || raw.SHA != b.logoSHA || raw.Commit != b.first {
		t.Errorf("Raw() = %+v with %q, %v", raw, body, err)
	}
	large, err := b.client.Raw(b.ctx(t), website, "main", "big.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = large.Body.Close() }()
	if n, err := io.Copy(io.Discard, large.Body); err != nil || n != code.MaxText+10 {
		t.Errorf("Raw() of a large file streamed %d bytes, %v", n, err)
	}
	if _, err := b.client.Raw(b.ctx(t), website, "main", "docs"); !errors.Is(err, code.ErrNotBlob) {
		t.Errorf("Raw() of a directory: %v", err)
	}
}

// mainLog is main's whole log, which TestInternalLog's cases take pages of: the merge with both its parents, then
// feature's commit, then the first.
func mainLog(t *testing.T, b *browsed) code.Log {
	t.Helper()
	all, err := b.client.Log(b.ctx(t), website, code.LogQuery{Ref: "main", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range all.Entries {
		names = append(names, e.SHA)
	}
	if all.Commit != b.merge || !slices.Equal(names, []string{b.merge, b.feature, b.first}) || !slices.Equal(all.Entries[0].Parents, []string{b.first, b.feature}) {
		t.Fatalf("Log(main) = %+v, want the merge with both its parents, then feature's commit, then the first, whose clock ran ahead", all)
	}
	return all
}

func TestInternalLog(t *testing.T) {
	t.Parallel()
	b := newBrowsed(t)
	all := mainLog(t, b)
	tests := []struct {
		name, ref, path, grep string
		skip, limit           int
		count                 bool
		wantCommit            string
		want                  []code.LogEntry
		wantTotal             int
		wantErr               error
	}{
		{name: "after a skip, at most the limit", ref: "main", skip: 1, limit: 1, wantCommit: "merge", want: all.Entries[1:2]},
		{name: "counted", ref: "main", skip: 1, limit: 1, count: true, wantCommit: "merge", want: all.Entries[1:2], wantTotal: 3},
		{name: "only counted", ref: "main", count: true, wantCommit: "merge", wantTotal: 3},
		{name: "the messages that hold a query, ignoring case", ref: "main", grep: "FEATURE", limit: 10, count: true, wantCommit: "merge",
			want: all.Entries[:2], wantTotal: 2},
		{name: "a query's page after a skip", ref: "main", grep: "feature", skip: 1, limit: 1, count: true, wantCommit: "merge",
			want: all.Entries[1:2], wantTotal: 2},
		{name: "a query is text, not a pattern", ref: "main", grep: "F.rst", limit: 10, count: true, wantCommit: "merge"},
		{name: "a query that looks like an option", ref: "main", grep: "--all", limit: 10, count: true, wantCommit: "merge"},
		{name: "a path's, queried and counted", ref: "main", path: "feature.txt", grep: "add", limit: 10, count: true, wantCommit: "merge",
			want: []code.LogEntry{{SHA: b.feature, Parents: []string{}}}, wantTotal: 1},
		{name: "a path's, counted", ref: "main", path: "docs", count: true, wantCommit: "merge", wantTotal: 1},
		{name: "a file's, the merge simplified away", ref: "main", path: "feature.txt", limit: 10, wantCommit: "merge",
			want: []code.LogEntry{{SHA: b.feature, Parents: []string{}}}},
		{name: "a directory's", ref: "main", path: "docs", limit: 10, wantCommit: "merge", want: []code.LogEntry{{SHA: b.first, Parents: []string{}}}},
		{name: "a path is a name, not a pattern", ref: "main", path: "doc*", limit: 10, wantCommit: "merge"},
		{name: "a path nothing ever held", ref: "main", path: "gone.txt", limit: 10, wantCommit: "merge"},
		{name: "past the end", ref: "main", skip: 3, limit: 10, wantCommit: "merge"},
		{name: "no commits, only the ref resolved", ref: "main", limit: 0, wantCommit: "merge"},
		{name: "an annotated tag, peeled", ref: "v1", limit: 10, wantCommit: "first", want: []code.LogEntry{{SHA: b.first, Parents: []string{}}}},
		{name: "a commit's full name", ref: "feature", limit: 10, wantCommit: "feature",
			want: []code.LogEntry{{SHA: b.feature, Parents: []string{b.first}}, {SHA: b.first, Parents: []string{}}}},
		{name: "a ref that names nothing", ref: "gone", limit: 10, wantErr: code.ErrRefNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := code.LogQuery{Ref: b.named(tt.ref), Path: tt.path, Grep: tt.grep, Skip: tt.skip, Limit: tt.limit, Count: tt.count}
			log, err := b.client.Log(b.ctx(t), website, q)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Log() error = %v, want %v", err, tt.wantErr)
			}
			if err == nil && (log.Commit != b.named(tt.wantCommit) || !sameLog(log.Entries, tt.want) || !counts(log.Total, tt.count, tt.wantTotal)) {
				t.Errorf("Log() = %v at %s, of %s, want %v at %s, of %d", log.Entries, log.Commit, countOf(log.Total), tt.want, b.named(tt.wantCommit), tt.wantTotal)
			}
		})
	}
	for _, query := range []string{"skip=-1&limit=1", "skip=0&limit=1001", "skip=x&limit=1", "skip=0"} {
		if status := b.status(t, "/log?ref=main&"+query); status != http.StatusBadRequest {
			t.Errorf("log?%s answered %d, want %d", query, status, http.StatusBadRequest)
		}
	}
}

func TestInternalLastCommits(t *testing.T) {
	t.Parallel()
	b := newBrowsed(t)
	tests := []struct {
		name, ref, path string
		want            code.LastCommits
		wantErr         error
	}{
		{name: "main's root: the merge is the latest, the feature's commit changed its file", ref: "main",
			want: code.LastCommits{Commit: "merge", Count: 3, Latest: "merge", Entries: map[string]string{"README.md": "first", "big.txt": "first",
				"bin": "first", "docs": "first", "feature.txt": "feature", "link": "first", "logo.png": "first", "vendor": "first"}}},
		{name: "a directory the merge didn't change", ref: "main", path: "docs",
			want: code.LastCommits{Commit: "merge", Path: "docs", Count: 1, Latest: "first", Entries: map[string]string{"*.md": "first", "guide.md": "first",
				"with space ü.md": "first"}}},
		{name: "at an annotated tag", ref: "v1", path: "bin",
			want: code.LastCommits{Commit: "first", Path: "bin", Count: 1, Latest: "first", Entries: map[string]string{"tool": "first"}}},
		{name: "a file's path", ref: "main", path: "README.md", wantErr: code.ErrNotTree},
		{name: "a submodule's path", ref: "main", path: "vendor/lib", wantErr: code.ErrSubmodule},
		{name: "a ref that names nothing", ref: "gone", wantErr: code.ErrRefNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := b.client.LastCommits(b.ctx(t), website, b.named(tt.ref), tt.path)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("LastCommits() error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			want := tt.want
			want.Commit, want.Latest, want.Entries = b.named(want.Commit), b.named(want.Latest), map[string]string{}
			for name, sha := range tt.want.Entries {
				want.Entries[name] = b.named(sha)
			}
			if got.Commit != want.Commit || got.Path != want.Path || got.Count != want.Count || got.Latest != want.Latest || !maps.Equal(got.Entries, want.Entries) {
				t.Errorf("LastCommits() = %+v, want %+v", got, want)
			}
		})
	}
}

func TestInternalBlame(t *testing.T) {
	t.Parallel()
	b := newBrowsed(t)
	mustGit(t, b.dir, "checkout", "-q", "-b", "blamed", b.merge)
	more := commit(t, b.dir, "README.md", "# Website\nMore\n")
	mustGit(t, b.dir, "push", "-q", "origin", "blamed")
	tests := []struct {
		name, ref, path string
		wantCommit      string
		want            []code.BlameRange
		wantErr         error
	}{
		{name: "a line each commit added, by line", ref: "blamed", path: "README.md", wantCommit: more,
			want: []code.BlameRange{{SHA: b.first, Start: 1, Lines: 1}, {SHA: more, Start: 2, Lines: 1}}},
		{name: "a file a merged branch added", ref: "main", path: "feature.txt", wantCommit: b.merge, want: []code.BlameRange{{SHA: b.feature, Start: 1, Lines: 1}}},
		{name: "a name Git's pathspecs would read as a pattern", ref: "v1", path: "docs/*.md", wantCommit: b.first,
			want: []code.BlameRange{{SHA: b.first, Start: 1, Lines: 1}}},
		{name: "a directory", ref: "main", path: "docs", wantErr: code.ErrNotBlob},
		{name: "a submodule", ref: "main", path: "vendor/lib", wantErr: code.ErrSubmodule},
		{name: "a path the commit doesn't hold", ref: "v1", path: "feature.txt", wantErr: code.ErrPathNotFound},
		{name: "a ref that names nothing", ref: "gone", path: "README.md", wantErr: code.ErrRefNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := b.client.Blame(b.ctx(t), website, tt.ref, tt.path)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Blame() error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.Commit != tt.wantCommit || got.Path != tt.path || !slices.Equal(got.Ranges, tt.want) {
				t.Errorf("Blame() = %+v, want commit %s and runs %+v", got, tt.wantCommit, tt.want)
			}
		})
	}
}

func TestInternalPaths(t *testing.T) {
	t.Parallel()
	b := newBrowsed(t)
	tests := []struct {
		name, ref  string
		wantCommit string
		want       []string
		wantErr    error
	}{
		{name: "every file and symlink, in Git's order, without the submodule", ref: "main", wantCommit: b.merge,
			want: []string{"README.md", "big.txt", "bin/tool", "docs/*.md", "docs/guide.md", "docs/with space ü.md", "feature.txt", "link", "logo.png"}},
		{name: "at an annotated tag", ref: "v1", wantCommit: b.first,
			want: []string{"README.md", "big.txt", "bin/tool", "docs/*.md", "docs/guide.md", "docs/with space ü.md", "link", "logo.png"}},
		{name: "a ref that names nothing", ref: "gone", wantErr: code.ErrRefNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := b.client.Paths(b.ctx(t), website, tt.ref)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Paths() error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.Commit != tt.wantCommit || got.Truncated || !slices.Equal(got.Paths, tt.want) {
				t.Errorf("Paths() = %+v, want commit %s and paths %q", got, tt.wantCommit, tt.want)
			}
		})
	}
}

// counts tells whether total is what a walk asked to count carries: want, or nil when it wasn't asked.
func counts(total *int, count bool, want int) bool {
	if !count {
		return total == nil
	}
	return total != nil && *total == want
}

// countOf is what total points at, or "none".
func countOf(total *int) string {
	if total == nil {
		return "none"
	}
	return strconv.Itoa(*total)
}

// sameLog reports whether got and want name the same commits with the same parents, in the same order.
func sameLog(got, want []code.LogEntry) bool {
	return slices.EqualFunc(got, want, func(a, b code.LogEntry) bool { return a.SHA == b.SHA && slices.Equal(a.Parents, b.Parents) })
}

// status is the status the internal call answers a read of the website repository with, path and query included, when
// it sends the reader's token.
func (b *browsed) status(t *testing.T, read string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, b.internal+gitinternal.ReposPath+website.String()+read, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+b.reader)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestInternalViewer(t *testing.T) {
	t.Parallel()
	b := newBrowsed(t)
	session, err := auth.NewService(postgres.NewAuthStore(b.stack.pool), nil, auth.Settings{Seed: true}).SignInSeeded(t.Context(), "guest", "/", auth.Client{})
	if err != nil {
		t.Fatal(err)
	}
	nia := uuid.MustParse("00000000-0000-7000-8000-00000000d004")
	tests := []struct {
		name, header string
		want         int
	}{
		{name: "no credential reads as Anonymous, who sees no private repository", want: http.StatusNotFound},
		{name: "a credential that acts as nobody", header: "Bearer " + auth.NewSecret(auth.PrefixPersonalToken), want: http.StatusUnauthorized},
		{name: "a Basic password", header: "Basic Z2l0OnRva2Vu", want: http.StatusUnauthorized},
		{name: "a token of someone outside the organization", header: "Bearer " + b.stack.token(nia), want: http.StatusNotFound},
		{name: "a reader's token", header: "Bearer " + b.reader, want: http.StatusOK},
		{name: "a reader's session", header: "Bearer " + session.SessionToken, want: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, b.internal+gitinternal.ReposPath+website.String()+"/tree?ref=main", nil)
			if err != nil {
				t.Fatal(err)
			}
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != tt.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
}
