package gitserver_test

import (
	"bytes"
	"context"
	"errors"
	"io"
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
	mustGit(t, b.dir, "commit", "-q", "-m", "First")
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

func TestInternalViewer(t *testing.T) {
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
