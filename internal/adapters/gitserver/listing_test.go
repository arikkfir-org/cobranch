package gitserver

import (
	"bufio"
	"slices"
	"strconv"
	"strings"
	"testing"

	"cobranch.dev/internal/services/code"
)

// tokensOf reads out as git's -z output.
func tokensOf(out string) *nulTokens {
	return &nulTokens{r: bufio.NewReaderSize(strings.NewReader(out), 16)}
}

func TestReadRaw(t *testing.T) {
	modified := ":100644 100644 aaaaaaa bbbbbbb M\x00a.txt\x00"
	renamed := ":100644 100644 aaaaaaa aaaaaaa R100\x00old.txt\x00new.txt\x00"
	added := ":000000 100755 0000000 ccccccc A\x00bin/tool\x00"
	deleted := ":100644 000000 ddddddd 0000000 D\x00gone.txt\x00"
	tests := []struct {
		name        string
		out         string
		limit       int
		want        []string
		wantStopped bool
		wantErr     string
	}{
		{name: "nothing", limit: 3},
		{name: "every record", out: modified + renamed + added, limit: 4,
			want: []string{"modified a.txt 100644>100644 aaaaaaa>bbbbbbb", "renamed old.txt>new.txt 100644>100644 aaaaaaa>aaaaaaa", "added bin/tool >100755 >ccccccc"}},
		{name: "a deleted file has no object after", out: deleted, limit: 3, want: []string{"deleted gone.txt 100644> ddddddd>"}},
		{name: "as many records as the limit", out: modified + renamed + added, limit: 3, wantStopped: true,
			want: []string{"modified a.txt 100644>100644 aaaaaaa>bbbbbbb", "renamed old.txt>new.txt 100644>100644 aaaaaaa>aaaaaaa", "added bin/tool >100755 >ccccccc"}},
		{name: "up to the limit", out: modified + renamed + added, limit: 2, wantStopped: true,
			want: []string{"modified a.txt 100644>100644 aaaaaaa>bbbbbbb", "renamed old.txt>new.txt 100644>100644 aaaaaaa>aaaaaaa"}},
		{name: "a path longer than the reader's buffer", out: ":100644 100644 aaaaaaa bbbbbbb M\x00" + strings.Repeat("d/", 40) + "f\x00", limit: 3,
			want: []string{"modified " + strings.Repeat("d/", 40) + "f 100644>100644 aaaaaaa>bbbbbbb"}},
		{name: "a record without its path", out: ":100644 100644 aaaaaaa bbbbbbb M\x00", limit: 3, wantErr: "ended inside record"},
		{name: "a rename without its new path", out: ":100644 100644 aaaaaaa aaaaaaa R100\x00old.txt\x00", limit: 3, wantErr: "ended inside record"},
		{name: "output cut inside a token", out: modified + ":100644", limit: 3, wantErr: "ended inside a token"},
		{name: "a patch after the records", out: modified + "\x00diff --git a/a.txt b/a.txt\n", limit: 3, wantErr: `printed ""`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, stopped, err := readRaw(tokensOf(tt.out), tt.limit)
			if failed(t, err, tt.wantErr) {
				return
			}
			got := rawSummary(files)
			if !slices.Equal(got, tt.want) || stopped != tt.wantStopped {
				t.Errorf("readRaw() = %q, stopped %t; want %q, stopped %t", got, stopped, tt.want, tt.wantStopped)
			}
		})
	}
}

func TestAddCounts(t *testing.T) {
	numstat := "3\t1\ta.txt\x00" + "0\t0\t\x00old.txt\x00new.txt\x00" + "-\t-\tlogo.png\x00" + "5\t0\tb.txt\x00"
	tests := []struct {
		name       string
		out        string
		listed     int
		limited    bool
		want       []string
		wantTotals diffTotals
		wantErr    string
	}{
		{name: "every file listed", out: numstat, listed: 4, want: []string{"+3-1", "+0-0", "binary", "+5-0"},
			wantTotals: diffTotals{files: 4, additions: 8, deletions: 1}},
		{name: "files past the limit counted, not kept", out: numstat, listed: 2, limited: true, want: []string{"+3-1", "+0-0"},
			wantTotals: diffTotals{files: 4, additions: 8, deletions: 1}},
		{name: "more files than raw listed below the limit", out: numstat, listed: 2, wantErr: "listed 4 files, and --raw 2"},
		{name: "fewer files than raw listed", out: numstat, listed: 5, wantErr: "listed 4 files, and --raw 5"},
		{name: "a rename without its paths", out: "0\t0\t\x00old.txt\x00", listed: 1, wantErr: "ended inside record"},
		{name: "a count that isn't one", out: "x\t1\ta.txt\x00", listed: 1, wantErr: "printed"},
		{name: "a record without its fields", out: "a.txt\x00", listed: 1, wantErr: "printed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := make([]code.FileDiff, tt.listed)
			totals, err := addCounts(tokensOf(tt.out), files, tt.limited)
			if failed(t, err, tt.wantErr) {
				return
			}
			got := countSummary(files)
			if !slices.Equal(got, tt.want) || totals != tt.wantTotals {
				t.Errorf("addCounts() = %q, %+v; want %q, %+v", got, totals, tt.want, tt.wantTotals)
			}
		})
	}
}

// failed checks err against wantErr, a part of the error wanted or "" for none, and answers whether there was one.
func failed(t *testing.T, err error, wantErr string) bool {
	t.Helper()
	switch {
	case wantErr == "" && err != nil:
		t.Fatal(err)
	case wantErr != "" && (err == nil || !strings.Contains(err.Error(), wantErr)):
		t.Fatalf("error = %v, want one saying %q", err, wantErr)
	}
	return err != nil
}

// rawSummary is each file's status, path, the old one before it for a rename, and modes.
func rawSummary(files []code.FileDiff) []string {
	var out []string
	for _, f := range files {
		path := f.Path
		if f.OldPath != "" {
			path = f.OldPath + ">" + f.Path
		}
		out = append(out, string(f.Status)+" "+path+" "+f.OldMode+">"+f.Mode+" "+f.OldObject+">"+f.Object)
	}
	return out
}

// countSummary is each file's counts, or binary.
func countSummary(files []code.FileDiff) []string {
	var out []string
	for _, f := range files {
		if f.Binary {
			out = append(out, "binary")
			continue
		}
		out = append(out, "+"+strconv.Itoa(f.Additions)+"-"+strconv.Itoa(f.Deletions))
	}
	return out
}
