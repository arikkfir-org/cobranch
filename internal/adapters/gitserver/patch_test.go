package gitserver

import (
	"bufio"
	"io"
	"strconv"
	"strings"
	"testing"

	"cobranch.dev/internal/services/code"
)

func TestPatchRead(t *testing.T) {
	long := strings.Repeat("y", code.MaxDiffLineBytes+1) // with its "+", as long as the reader's buffer
	wide := strings.Repeat("w", 60000)                   // 17 of them fit in a file's text, and 4 such files in a diff's
	tests := []struct {
		name string
		// patch is each file's lines added, a nil one for a binary file, which has no hunks.
		patch [][]string
		want  []string
		// wantStopped says the reading stopped before the patch's end.
		wantStopped bool
	}{
		{name: "files within the limits", patch: [][]string{{"one", "two"}, nil, {"three"}},
			want: []string{"2 lines", "binary", "1 lines"}},
		{name: "a file past its limit, then one within", patch: [][]string{repeatLines(code.MaxFileDiffLines+1, "x"), {"one"}},
			want: []string{"left out", "1 lines"}},
		{name: "a line too long, then a file", patch: [][]string{{"short", strings.Repeat("y", code.MaxDiffLineBytes+10), "short"}, {"one"}},
			want: []string{"left out", "1 lines"}},
		{name: "the rest of a long line read as a line's, not a patch's start", patch: [][]string{{long + "diff --git a/z b/z"}, {"one"}},
			want: []string{"left out", "1 lines"}},
		{name: "lines past the diff's in all", wantStopped: true,
			patch: [][]string{repeatLines(4500, "x"), repeatLines(4500, "x"), repeatLines(4500, "x"), repeatLines(4500, "x"),
				repeatLines(4999, strings.Repeat("x", 100)), {"one"}},
			want: []string{"4500 lines", "4500 lines", "4500 lines", "4500 lines", "left out", "left out"}},
		{name: "a file past its text, then one within", patch: [][]string{repeatLines(18, wide), {"one"}},
			want: []string{"left out", "1 lines"}},
		{name: "text past the diff's in all, in lines within every other limit", wantStopped: true,
			patch: [][]string{repeatLines(17, wide), repeatLines(17, wide), repeatLines(17, wide), repeatLines(17, wide),
				repeatLines(17, wide), {"one"}},
			want: []string{"17 lines", "17 lines", "17 lines", "17 lines", "left out", "left out"}},
		{name: "skipping no further than its budget", wantStopped: true,
			patch: [][]string{repeatLines(code.MaxFileDiffLines+maxSkippedBytes/1000+1000, strings.Repeat("x", 1000)), {"one"}, nil},
			want:  []string{"left out", "left out", "binary"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, files := patchOf(tt.patch)
			r := &countingReader{r: strings.NewReader(text)}
			p := &patch{files: files}
			if err := p.read(bufio.NewReaderSize(r, code.MaxDiffLineBytes+2)); err != nil {
				t.Fatal(err)
			}
			p.leaveOut()
			got := hunkSummary(p.files)
			stopped := r.n < len(text)
			if strings.Join(got, ", ") != strings.Join(tt.want, ", ") || stopped != tt.wantStopped {
				t.Errorf("read() = %q, stopped %t; want %q, stopped %t", got, stopped, tt.want, tt.wantStopped)
			}
		})
	}
}

// repeatLines is n lines of text.
func repeatLines(n int, text string) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = text
	}
	return lines
}

// patchOf is git diff's patch of files numbered from 0, each adding its lines, or binary for nil, and the files as the
// raw and numstat listings give them.
func patchOf(patch [][]string) (string, []code.FileDiff) {
	var b strings.Builder
	files := make([]code.FileDiff, len(patch))
	for i, lines := range patch {
		path := "f" + strconv.Itoa(i)
		files[i] = code.FileDiff{Status: code.FileAdded, Path: path, Mode: "100644", Additions: len(lines), Binary: lines == nil}
		b.WriteString("diff --git a/" + path + " b/" + path + "\nnew file mode 100644\nindex 0000000..1111111\n")
		if lines == nil {
			b.WriteString("Binary files /dev/null and b/" + path + " differ\n")
			continue
		}
		b.WriteString("--- /dev/null\n+++ b/" + path + "\n@@ -0,0 +1," + strconv.Itoa(len(lines)) + " @@\n")
		for _, l := range lines {
			b.WriteString("+" + l + "\n")
		}
	}
	return b.String(), files
}

// hunkSummary is each file's lines kept, or whether it was left out or is binary.
func hunkSummary(files []code.FileDiff) []string {
	var out []string
	for _, f := range files {
		switch {
		case f.Truncated:
			out = append(out, "left out")
		case f.Binary:
			out = append(out, "binary")
		default:
			n := 0
			for _, h := range f.Hunks {
				n += len(h.Lines)
			}
			out = append(out, strconv.Itoa(n)+" lines")
		}
	}
	return out
}

// countingReader counts the bytes read from r.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	c.n += n
	return n, err //nolint:wrapcheck // a test's reader passes its source's errors on as they are
}
