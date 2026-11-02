package gitserver_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"cobranch.dev/internal/services/code"
)

// diffed is the browsed repository with the commits the diff's tests read: prepared, on main's merge, adds a file
// with a section heading Git finds; changes, on prepared, changes files every way a diff tells, and main gains later
// after it; limits passes every limit of a diff's hunks, and many the limit of its files; and unrelated shares no
// history with main.
type diffed struct {
	*browsed
	prepared, changes, later, limits, many, unrelated string
}

func newDiffed(t *testing.T) *diffed {
	t.Helper()
	d := &diffed{browsed: newBrowsed(t)}
	dir := d.dir
	mustGit(t, dir, "checkout", "-q", "--detach", d.merge)
	d.prepared = commit(t, dir, "code.txt", "func main\n"+numbered("  line %d\n", 2, 12))
	write(t, dir, "code.txt", "func main\n"+numbered("  line %d\n", 2, 8)+"  line nine\n"+numbered("  line %d\n", 10, 12))
	write(t, dir, "README.md", "# Website\nNo newline")
	write(t, dir, "logo.png", "\x89PNG\r\n\x1a\n\x00\x02")
	write(t, dir, "new.txt", "One\nTwo\n")
	mustGit(t, dir, "mv", "docs/guide.md", "docs/manual.md")
	mustGit(t, dir, "rm", "-q", "big.txt", "link")
	write(t, dir, "link", "Now a file\n")
	if err := os.Chmod(filepath.Join(dir, "bin/tool"), 0o600); err != nil {
		t.Fatal(err)
	}
	d.changes = commitPaths(t, dir, "Change", "README.md", "logo.png", "new.txt", "link", "code.txt", "bin/tool")
	mustGit(t, dir, "checkout", "-q", "--detach", d.prepared)
	d.later = commit(t, dir, "main.txt", "Later\n")
	d.limits = d.commitLimits(t)
	d.many = d.commitMany(t)
	mustGit(t, dir, "checkout", "-q", "--orphan", "unrelated")
	mustGit(t, dir, "read-tree", "--empty")
	write(t, dir, "only.txt", "Alone\n")
	mustGit(t, dir, "add", "only.txt")
	mustGit(t, dir, "commit", "-q", "-m", "Unrelated")
	d.unrelated = strings.TrimSpace(mustGit(t, dir, "rev-parse", "HEAD"))
	mustGit(t, dir, "push", "-q", "-f", "origin", d.later+":refs/heads/main", d.changes+":refs/heads/changes",
		d.limits+":refs/heads/limits", d.many+":refs/heads/many", d.unrelated+":refs/heads/unrelated")
	return d
}

// commitLimits commits, on main's merge, a file past MaxFileDiffLines, then four that fill all but 2000 of
// MaxDiffLines and a fifth that passes it, and after them a binary file, an empty file and a file the reading never
// reaches.
func (d *diffed) commitLimits(t *testing.T) string {
	t.Helper()
	mustGit(t, d.dir, "checkout", "-q", "--detach", d.merge)
	write(t, d.dir, "0long.txt", numbered("%d\n", 1, code.MaxFileDiffLines+1))
	for i := 1; i <= 5; i++ {
		write(t, d.dir, fmt.Sprintf("a%d.txt", i), numbered("%d\n", 1, 4500))
	}
	write(t, d.dir, "z.bin", "\x00\x01\x02")
	write(t, d.dir, "zempty.txt", "")
	write(t, d.dir, "zz.txt", "Left out\n")
	return commitPaths(t, d.dir, "Limits", "0long.txt", "a1.txt", "a2.txt", "a3.txt", "a4.txt", "a5.txt", "z.bin", "zempty.txt", "zz.txt")
}

// commitMany commits, on main's merge, one file more than MaxDiffFiles.
func (d *diffed) commitMany(t *testing.T) string {
	t.Helper()
	mustGit(t, d.dir, "checkout", "-q", "--detach", d.merge)
	for i := range code.MaxDiffFiles + 1 {
		write(t, d.dir, fmt.Sprintf("many/f%04d", i), "x\n")
	}
	return commitPaths(t, d.dir, "Many", "many")
}

// commitPaths commits what changed at paths in dir's working tree, with what the index holds already, and returns
// the commit. Adding everything instead would delete main's submodule, which has no directory in the working tree.
func commitPaths(t *testing.T, dir, message string, paths ...string) string {
	t.Helper()
	mustGit(t, dir, append([]string{"add", "--"}, paths...)...)
	mustGit(t, dir, "commit", "-q", "-m", message)
	return strings.TrimSpace(mustGit(t, dir, "rev-parse", "HEAD"))
}

// numbered is format written for each number from first to last.
func numbered(format string, first, last int) string {
	var b strings.Builder
	for i := first; i <= last; i++ {
		_, _ = fmt.Fprintf(&b, format, i) // a Builder takes everything
	}
	return b.String()
}

// named is the commit a test's name stands for, or the name itself.
func (d *diffed) named(name string) string {
	switch name {
	case "prepared":
		return d.prepared
	case "changes":
		return d.changes
	case "later":
		return d.later
	case "limits":
		return d.limits
	case "many":
		return d.many
	case "unrelated":
		return d.unrelated
	}
	return d.browsed.named(name)
}

func TestInternalDiff(t *testing.T) {
	t.Parallel()
	d := newDiffed(t)
	changed := []string{"modified README.md 100644>100644 +1-0 hunks 1", "deleted big.txt 100644> +0-1 truncated hunks 0",
		"modified bin/tool 100755>100644 +0-0 hunks 0", "modified code.txt 100644>100644 +1-1 hunks 1",
		"renamed docs/manual.md 100644>100644 +0-0 from docs/guide.md hunks 0", "type_changed link 120000>100644 +1-1 hunks 2",
		"modified logo.png 100644>100644 +0-0 binary hunks 0", "added new.txt >100644 +2-0 hunks 1"}
	tests := []struct {
		name                         string
		req                          code.DiffRequest
		wantBase, wantBaseTip        string
		want                         []string
		wantFiles, wantLeftOut       int
		wantAdditions, wantDeletions int
		wantErr                      error
	}{
		{name: "a commit, against its parent", req: code.DiffRequest{Base: "prepared", Head: "changes"}, wantBase: "prepared",
			want: changed, wantAdditions: 5, wantDeletions: 3},
		{name: "a branch, from its merge base with its base's tip", req: code.DiffRequest{Base: "main", Head: "changes", MergeBase: true},
			wantBase: "prepared", wantBaseTip: "later", want: changed, wantAdditions: 5, wantDeletions: 3},
		{name: "a root commit, against the empty tree", req: code.DiffRequest{Head: "first"},
			want: []string{"added README.md >100644 +1-0 hunks 1", "added big.txt >100644 +1-0 truncated hunks 0", "added bin/tool >100755 +1-0 hunks 1",
				"added docs/*.md >100644 +1-0 hunks 1", "added docs/guide.md >100644 +1-0 hunks 1", "added docs/with space ü.md >100644 +1-0 hunks 1",
				"added link >120000 +1-0 hunks 1", "added logo.png >100644 +0-0 binary hunks 0", "added vendor/lib >160000 +1-0 hunks 1"},
			wantAdditions: 8},
		{name: "a branch merged into its base shows nothing", req: code.DiffRequest{Base: "main", Head: "feature", MergeBase: true},
			wantBase: "feature", wantBaseTip: "later"},
		{name: "a branch that shares no history, against the empty tree", req: code.DiffRequest{Base: "main", Head: "unrelated", MergeBase: true},
			wantBaseTip: "later", want: []string{"added only.txt >100644 +1-0 hunks 1"}, wantAdditions: 1},
		{name: "hunks past the limits", req: code.DiffRequest{Base: "merge", Head: "limits"}, wantBase: "merge",
			want: []string{"added 0long.txt >100644 +5001-0 truncated hunks 0", "added a1.txt >100644 +4500-0 hunks 1", "added a2.txt >100644 +4500-0 hunks 1",
				"added a3.txt >100644 +4500-0 hunks 1", "added a4.txt >100644 +4500-0 hunks 1", "added a5.txt >100644 +4500-0 truncated hunks 0",
				"added z.bin >100644 +0-0 binary hunks 0", "added zempty.txt >100644 +0-0 hunks 0", "added zz.txt >100644 +1-0 truncated hunks 0"},
			wantAdditions: 5001 + 5*4500 + 1},
		{name: "files past the limit", req: code.DiffRequest{Base: "merge", Head: "many"}, wantBase: "merge",
			wantFiles: code.MaxDiffFiles, wantLeftOut: 1, wantAdditions: code.MaxDiffFiles + 1},
		{name: "a base that names nothing", req: code.DiffRequest{Base: "gone", Head: "changes"}, wantErr: code.ErrRefNotFound},
		{name: "a head that names nothing", req: code.DiffRequest{Head: "gone"}, wantErr: code.ErrRefNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := tt.req
			req.Base, req.Head = d.named(req.Base), d.named(req.Head)
			got, err := d.client.Diff(d.ctx(t), website, req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Diff() error = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.Base != d.named(tt.wantBase) || got.BaseTip != d.named(tt.wantBaseTip) || got.Head != req.Head {
				t.Errorf("Diff() from %q (tip %q) to %q, want from %q (tip %q) to %q", got.Base, got.BaseTip, got.Head, tt.wantBase,
					tt.wantBaseTip, tt.req.Head)
			}
			checkFiles(t, got, tt.want, tt.wantFiles)
			if got.FilesLeftOut != tt.wantLeftOut || got.Additions != tt.wantAdditions || got.Deletions != tt.wantDeletions {
				t.Errorf("Diff() left out %d files, +%d-%d, want %d, +%d-%d", got.FilesLeftOut, got.Additions, got.Deletions,
					tt.wantLeftOut, tt.wantAdditions, tt.wantDeletions)
			}
		})
	}
}

func TestInternalDiffHunks(t *testing.T) {
	t.Parallel()
	d := newDiffed(t)
	got, err := d.client.Diff(d.ctx(t), website, code.DiffRequest{Base: d.prepared, Head: d.changes})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path string
		want []string
	}{
		{path: "README.md", want: []string{"-1,1 +1,2", " 1,1 # Website", "+0,2 No newline (no newline)"}},
		{path: "code.txt", want: []string{"-6,7 +6,7 func main", " 6,6   line 6", " 7,7   line 7", " 8,8   line 8", "-9,0   line 9",
			"+0,9   line nine", " 10,10   line 10", " 11,11   line 11", " 12,12   line 12"}},
		{path: "link", want: []string{"-1,1 +0,0", "-1,0 docs/guide.md (no newline)", "-0,0 +1,1", "+0,1 Now a file"}},
		{path: "new.txt", want: []string{"-0,0 +1,2", "+0,1 One", "+0,2 Two"}},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			i := slices.IndexFunc(got.Files, func(f code.FileDiff) bool { return f.Path == tt.path })
			if i < 0 {
				t.Fatalf("Diff() lists no %s", tt.path)
			}
			var lines []string
			for _, h := range got.Files[i].Hunks {
				lines = append(lines, strings.TrimSpace(fmt.Sprintf("-%d,%d +%d,%d %s", h.OldStart, h.OldLines, h.NewStart, h.NewLines, h.Section)))
				for _, l := range h.Lines {
					lines = append(lines, lineSummary(l))
				}
			}
			if !slices.Equal(lines, tt.want) {
				t.Errorf("%s's hunks =\n%s\nwant\n%s", tt.path, strings.Join(lines, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

// checkFiles checks that d lists the files want summarizes, or, without want, wantFiles files.
func checkFiles(t *testing.T, d code.Diff, want []string, wantFiles int) {
	t.Helper()
	if want == nil && wantFiles > 0 {
		if len(d.Files) != wantFiles {
			t.Errorf("Diff() lists %d files, want %d", len(d.Files), wantFiles)
		}
		return
	}
	var files []string
	for _, f := range d.Files {
		files = append(files, fileSummary(f))
	}
	if !slices.Equal(files, want) {
		t.Errorf("Diff() files =\n%s\nwant\n%s", strings.Join(files, "\n"), strings.Join(want, "\n"))
	}
}

// fileSummary is a file's change in a line a test compares.
func fileSummary(f code.FileDiff) string {
	s := fmt.Sprintf("%s %s %s>%s +%d-%d", f.Status, f.Path, f.OldMode, f.Mode, f.Additions, f.Deletions)
	if f.OldPath != "" {
		s += " from " + f.OldPath
	}
	if f.Binary {
		s += " binary"
	}
	if f.Truncated {
		s += " truncated"
	}
	return s + " hunks " + strconv.Itoa(len(f.Hunks))
}

// lineSummary is a hunk's line in a line a test compares: its kind as Git marks it, its numbers and its text.
func lineSummary(l code.DiffLine) string {
	mark := map[code.LineKind]string{code.LineContext: " ", code.LineAdded: "+", code.LineRemoved: "-"}[l.Kind]
	s := fmt.Sprintf("%s%d,%d %s", mark, l.Old, l.New, l.Text)
	if l.NoNewline {
		s += " (no newline)"
	}
	return s
}
