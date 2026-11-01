package code_test

import (
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/code/codetest"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

// withFiles gives the fixture's main a commit of files, and tag v1 the same commit.
func withFiles(f *fixture) *fixture {
	f.objects.Commit(sha(5), map[string]string{
		"README.md": "# Hello\n", "a.txt": "a\n", "Z.txt": "z\n", "src/main.go": "package main\n", "lib/util.go": "package lib\n",
		"deps" + codetest.Submodule: sha(7), "logo.png": "\x89PNG\r\n\x1a\n\x00", "big.txt": "a" + strings.Repeat("é", code.MaxText),
	}, "main", "v1")
	return f
}

func TestTags(t *testing.T) {
	tests := []struct {
		name    string
		viewer  func(*fixture) code.Viewer
		want    []string
		wantErr error
	}{
		{name: "the store's tags", viewer: func(f *fixture) code.Viewer { return f.max }, want: []string{"v1", "v2"}},
		{name: "a viewer who doesn't read the repository finds none", viewer: func(f *fixture) code.Viewer { return f.nia }, wantErr: repos.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			f.store.TagList[f.repo] = []code.Tag{{Name: "v1", Target: sha(1)}, {Name: "v2", Target: sha(5)}}
			got, err := f.service.Tags(t.Context(), tt.viewer(f), f.repo)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			var names []string
			for _, tag := range got {
				names = append(names, tag.Name)
			}
			if !slices.Equal(names, tt.want) {
				t.Errorf("got %v, want %v", names, tt.want)
			}
		})
	}
}

func TestTree(t *testing.T) {
	invalid := func(code string) error { return &orgs.ValidationError{Code: code} }
	tests := []struct {
		name    string
		viewer  func(*fixture) code.Viewer
		ref     string
		path    string
		fail    error
		want    []string
		wantErr error
	}{
		{name: "directories and submodules first, then files, each by name", ref: "main",
			want: []string{"deps.module", "lib", "src", "README.md", "Z.txt", "a.txt", "big.txt", "logo.png"}},
		{name: "a directory, at a tag", ref: "v1", path: "src", want: []string{"main.go"}},
		{name: "at a commit's full name", ref: sha(5), path: "lib", want: []string{"util.go"}},
		{name: "a ref that names nothing", ref: "gone", wantErr: code.ErrRefNotFound},
		{name: "an abbreviated name is a branch's that isn't there", ref: sha(5)[:12], wantErr: code.ErrRefNotFound},
		{name: "a ref with Git's revision syntax", ref: "main^2", wantErr: invalid("ref.invalid")},
		{name: "a ref of the reflog", ref: "main@{1}", wantErr: invalid("ref.invalid")},
		{name: "a ref like an option", ref: "-h", wantErr: invalid("ref.invalid")},
		{name: "a path that climbs out", ref: "main", path: "src/../..", wantErr: invalid("path.invalid")},
		{name: "a path from the root", ref: "main", path: "/src", wantErr: invalid("path.invalid")},
		{name: "a path with a trailing slash", ref: "main", path: "src/", wantErr: invalid("path.invalid")},
		{name: "a file's path", ref: "main", path: "a.txt", wantErr: code.ErrNotTree},
		{name: "a submodule's path", ref: "main", path: "deps.module", wantErr: code.ErrSubmodule},
		{name: "a path that holds nothing", ref: "main", path: "nothing", wantErr: code.ErrPathNotFound},
		{name: "a viewer who doesn't read the repository finds none", viewer: func(f *fixture) code.Viewer { return f.nia }, ref: "main", wantErr: repos.ErrNotFound},
		{name: "the Git server's failure", ref: "main", fail: errors.New("down"), wantErr: errors.New("down")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := withFiles(newFixture())
			f.objects.Fail = tt.fail
			v := f.max
			if tt.viewer != nil {
				v = tt.viewer(f)
			}
			got, err := f.service.Tree(t.Context(), v, f.repo, tt.ref, tt.path)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			var names []string
			for _, e := range got.Entries {
				names = append(names, e.Name)
			}
			if !slices.Equal(names, tt.want) {
				t.Errorf("got %v, want %v", names, tt.want)
			}
		})
	}
}

func TestFile(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		wantText      string
		wantBinary    bool
		wantTruncated bool
		wantErr       error
	}{
		{name: "a text file", path: "README.md", wantText: "# Hello\n"},
		{name: "a binary file has no text", path: "logo.png", wantBinary: true},
		{name: "a large file stops at a whole character", path: "big.txt", wantText: "a" + strings.Repeat("é", (code.MaxText-1)/2), wantTruncated: true},
		{name: "the root is no file", path: "", wantErr: code.ErrNotBlob},
		{name: "a directory is no file", path: "src", wantErr: code.ErrNotBlob},
		{name: "a submodule is no file", path: "deps.module", wantErr: code.ErrSubmodule},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := withFiles(newFixture())
			got, err := f.service.File(t.Context(), f.max, f.repo, "main", tt.path)
			if !sameError(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			if got.Text != tt.wantText || got.Binary != tt.wantBinary || got.Truncated != tt.wantTruncated {
				t.Errorf("got text of %d bytes, binary %v, truncated %v", len(got.Text), got.Binary, got.Truncated)
			}
			if err == nil && (got.Name != tt.path[strings.LastIndex(tt.path, "/")+1:] || got.Commit != sha(5)) {
				t.Errorf("got name %q at %s", got.Name, got.Commit)
			}
		})
	}
}

func TestRaw(t *testing.T) {
	f := withFiles(newFixture())
	raw, err := f.service.Raw(t.Context(), f.max, f.repo, "v1", "src/main.go")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Body.Close() }()
	body, err := io.ReadAll(raw.Body)
	if err != nil || string(body) != "package main\n" || raw.Size != 13 || raw.Commit != sha(5) {
		t.Errorf("Raw() = %+v, %q, %v", raw, body, err)
	}
	if _, err := f.service.Raw(t.Context(), f.nia, f.repo, "main", "README.md"); !errors.Is(err, repos.ErrNotFound) {
		t.Errorf("Raw() for a viewer who doesn't read the repository: %v", err)
	}
	if _, err := f.service.Raw(t.Context(), f.max, f.repo, "main", ""); !errors.Is(err, code.ErrNotBlob) {
		t.Errorf("Raw() of the root: %v", err)
	}
}

func TestWithoutGitServer(t *testing.T) {
	f := newFixture()
	f.service = code.NewService(f.store, nil, nil, f.authz)
	if _, err := f.service.Tree(t.Context(), f.max, f.repo, "main", ""); !errors.Is(err, code.ErrUnavailable) {
		t.Errorf("Tree() without a Git server: %v, want ErrUnavailable", err)
	}
}

func TestBinary(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		truncated bool
		want      bool
	}{
		{name: "text", content: "hello\n"},
		{name: "empty", content: ""},
		{name: "UTF-8", content: "שלום 👋"},
		{name: "a NUL", content: "a\x00b", want: true},
		{name: "a NUL past the probe", content: strings.Repeat("a", 8000) + "\x00"},
		{name: "Latin-1", content: "caf\xe9", want: true},
		{name: "a character cut by the limit", content: "👋"[:3], truncated: true},
		{name: "a whole file cut short isn't text", content: "👋"[:3], want: true},
		{name: "invalid bytes before the cut", content: "\xff" + "abc", truncated: true, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := code.Binary([]byte(tt.content), tt.truncated); got != tt.want {
				t.Errorf("Binary(%q, %v) = %v, want %v", tt.content, tt.truncated, got, tt.want)
			}
		})
	}
}

func TestValidateRef(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{ref: "main", want: true}, {ref: "feature/x", want: true}, {ref: "v1.0.0", want: true}, {ref: sha(1), want: true},
		{ref: strings.Repeat("a", 64), want: true}, {ref: ""}, {ref: "-h"}, {ref: "a..b"}, {ref: "a b"}, {ref: "a~1"}, {ref: "a^"},
		{ref: "a:b"}, {ref: "a?"}, {ref: "a*"}, {ref: "a[b"}, {ref: `a\b`}, {ref: "a@{1}"}, {ref: "@"}, {ref: "a/"}, {ref: "/a"},
		{ref: "a//b"}, {ref: "a."}, {ref: "a.lock"}, {ref: ".a"}, {ref: "a/.b"}, {ref: "a\x01"}, {ref: strings.Repeat("a", 256)},
	}
	for _, tt := range tests {
		if got := code.ValidateRef(tt.ref) == nil; got != tt.want {
			t.Errorf("ValidateRef(%q) passes = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestValidatePath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "", want: true}, {path: "a", want: true}, {path: "a/b.c", want: true}, {path: "a b/ü", want: true}, {path: ".github/x", want: true},
		{path: "/a"}, {path: "a/"}, {path: "a//b"}, {path: "."}, {path: "a/./b"}, {path: ".."}, {path: "a/../b"}, {path: "a\x00"},
		{path: "\xff"}, {path: strings.Repeat("a", 4097)},
	}
	for _, tt := range tests {
		if got := code.ValidatePath(tt.path) == nil; got != tt.want {
			t.Errorf("ValidatePath(%q) passes = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestTreeREADME(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string]string
		path      string
		renderer  bool
		fail      error
		wantPath  string
		wantText  string
		wantHTML  string
		wantNone  bool
		wantError error
	}{
		{name: "the root's README renders", files: map[string]string{"README.md": "# Hi\n", "a.txt": "a\n"}, renderer: true,
			wantPath: "README.md", wantText: "# Hi\n", wantHTML: `<div data-doc="acme/website@main:` + sha(5) + `:README.md"># Hi` + "\n</div>"},
		{name: "a directory's, whatever its case", files: map[string]string{"docs/ReadMe.MD": "Docs\n"}, path: "docs", renderer: true,
			wantPath: "docs/ReadMe.MD", wantText: "Docs\n", wantHTML: `<div data-doc="acme/website@main:` + sha(5) + `:docs/ReadMe.MD">Docs` + "\n</div>"},
		{name: "Markdown before text", files: map[string]string{"README.txt": "Text\n", "README.markdown": "Markdown\n"}, renderer: true,
			wantPath: "README.markdown", wantText: "Markdown\n", wantHTML: `<div data-doc="acme/website@main:` + sha(5) + `:README.markdown">Markdown` + "\n</div>"},
		{name: "a text README shows as text", files: map[string]string{"README": "Plain\n"}, renderer: true, wantPath: "README", wantText: "Plain\n"},
		{name: "without a renderer, Markdown shows as text", files: map[string]string{"README.md": "# Hi\n"}, wantPath: "README.md", wantText: "# Hi\n"},
		{name: "a binary README is none", files: map[string]string{"README": "\x00\x01"}, renderer: true, wantNone: true},
		{name: "a directory named README is none", files: map[string]string{"README/x.md": "x\n"}, renderer: true, wantNone: true},
		{name: "a directory without one", files: map[string]string{"a.txt": "a\n"}, renderer: true, wantNone: true},
		{name: "the renderer's failure", files: map[string]string{"README.md": "# Hi\n"}, renderer: true, fail: errors.New("broken"), wantError: errors.New("broken")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			f.objects.Commit(sha(5), tt.files, "main")
			f.markdown.Fail = tt.fail
			if !tt.renderer {
				f.service = code.NewService(f.store, f.objects, nil, f.authz)
			}
			got, err := f.service.Tree(t.Context(), f.max, f.repo, "main", tt.path)
			if !sameError(err, tt.wantError) {
				t.Fatalf("got error %v, want %v", err, tt.wantError)
			}
			if err == nil {
				checkREADME(t, got.README, tt.wantNone, code.File{Path: tt.wantPath, Text: tt.wantText, HTML: tt.wantHTML})
			}
		})
	}
}

// checkREADME checks that got is no README when none is wanted, or else want's path, text and HTML.
func checkREADME(t *testing.T, got *code.File, none bool, want code.File) {
	t.Helper()
	switch {
	case none && got != nil:
		t.Errorf("got README %+v, want none", got)
	case none:
	case got == nil:
		t.Error("got no README")
	case got.Path != want.Path || got.Text != want.Text || got.HTML != want.HTML:
		t.Errorf("got README %q with text %q and HTML %q", got.Path, got.Text, got.HTML)
	}
}

func TestFileMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		ref      string
		path     string
		wantHTML string
	}{
		{name: "a Markdown file renders at the ref it was asked at", ref: "v1", path: "docs/guide.mkdn",
			wantHTML: `<div data-doc="acme/website@v1:` + sha(5) + `:docs/guide.mkdn">Guide` + "\n</div>"},
		{name: "a text file doesn't", ref: "main", path: "a.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture()
			f.objects.Commit(sha(5), map[string]string{"docs/guide.mkdn": "Guide\n", "a.txt": "a\n"}, "main", "v1")
			got, err := f.service.File(t.Context(), f.max, f.repo, tt.ref, tt.path)
			if err != nil {
				t.Fatal(err)
			}
			if got.HTML != tt.wantHTML {
				t.Errorf("got HTML %q, want %q", got.HTML, tt.wantHTML)
			}
		})
	}
}

func TestIsMarkdown(t *testing.T) {
	for p, want := range map[string]bool{"README.md": true, "a/b.MARKDOWN": true, "x.mdown": true, "x.mkd": true, "x.mkdn": true,
		"x.txt": false, "md": false, "x.md.txt": false, "x.mdx": false} {
		if got := code.IsMarkdown(p); got != want {
			t.Errorf("IsMarkdown(%q) = %v, want %v", p, got, want)
		}
	}
}
