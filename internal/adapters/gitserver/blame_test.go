package gitserver

import (
	"bufio"
	"slices"
	"strconv"
	"strings"
	"testing"

	"cobranch.dev/internal/services/code"
)

func TestParseBlame(t *testing.T) {
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	run := func(sha, start, lines string, facts ...string) string {
		return sha + " 1 " + start + " " + lines + "\n" + strings.Join(append(facts, "filename README.md"), "\n") + "\n"
	}
	tests := []struct {
		name    string
		out     string
		want    []code.BlameRange
		wantErr bool
	}{
		{name: "an empty file", want: []code.BlameRange{}},
		{name: "runs by line, whatever order git finds them in",
			out:  run(b, "3", "2", "author Bea", "summary filename b.txt") + run(a, "1", "2", "author Al", "summary First") + run(a, "5", "1"),
			want: []code.BlameRange{{SHA: a, Start: 1, Lines: 2}, {SHA: b, Start: 3, Lines: 2}, {SHA: a, Start: 5, Lines: 1}}},
		{name: "neighbours one commit changed, joined", out: run(a, "3", "1") + run(a, "1", "2") + run(b, "4", "1"),
			want: []code.BlameRange{{SHA: a, Start: 1, Lines: 3}, {SHA: b, Start: 4, Lines: 1}}},
		{name: "a run that doesn't end", out: a + " 1 1 1\nauthor Al\n", wantErr: true},
		{name: "a run without its line", out: a + " 1 1\nfilename x\n", wantErr: true},
		{name: "a run of no lines", out: run(a, "1", "0"), wantErr: true},
		{name: "a name that isn't a commit's", out: run("HEAD", "1", "1"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseBlame([]byte(tt.out))
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseBlame() error = %v, want an error: %t", err, tt.wantErr)
			}
			if err == nil && !slices.Equal(got, tt.want) {
				t.Errorf("parseBlame() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestReadPaths(t *testing.T) {
	entry := func(mode, kind, path string) string {
		return mode + " " + kind + " " + strings.Repeat("c", 40) + "\t" + path + "\x00"
	}
	many := make([]string, 0, code.MaxPaths+1)
	var listing strings.Builder
	for i := range code.MaxPaths + 1 {
		many = append(many, "f"+strconv.Itoa(i))
		listing.WriteString(entry("100644", "blob", many[i]))
	}
	tests := []struct {
		name          string
		out           string
		want          []string
		wantTruncated bool
		wantErr       bool
	}{
		{name: "files and symlinks, without submodules",
			out:  entry("100644", "blob", "a b.txt") + entry("120000", "blob", "link") + entry("160000", "commit", "vendor/lib") + entry("100755", "blob", "bin/tool"),
			want: []string{"a b.txt", "link", "bin/tool"}},
		{name: "an empty tree", want: []string{}},
		{name: "a tree larger than the limit, up to it", out: listing.String(), want: many[:code.MaxPaths], wantTruncated: true},
		{name: "an entry without its path", out: "100644 blob " + strings.Repeat("c", 40) + "\x00", wantErr: true},
		{name: "an entry cut short", out: entry("100644", "blob", "a")[:20], wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, truncated, err := readPaths(bufio.NewReader(strings.NewReader(tt.out)))
			if (err != nil) != tt.wantErr {
				t.Fatalf("readPaths() error = %v, want an error: %t", err, tt.wantErr)
			}
			if err == nil && (truncated != tt.wantTruncated || !slices.Equal(got, tt.want)) {
				t.Errorf("readPaths() = %d paths, truncated %t, want %d, %t", len(got), truncated, len(tt.want), tt.wantTruncated)
			}
		})
	}
}
