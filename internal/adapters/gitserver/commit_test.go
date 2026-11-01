package gitserver

import (
	"strings"
	"testing"
	"time"

	"cobranch.dev/internal/services/git"
)

var (
	tree   = strings.Repeat("t", 40)
	parent = strings.Repeat("p", 40)
	other  = strings.Repeat("o", 40)
)

func TestParseCommit(t *testing.T) {
	signature := "-----BEGIN PGP SIGNATURE-----\n\niQEzBAABCAAdFiEE\n-----END PGP SIGNATURE-----"
	signed := "tree " + tree + "\nparent " + parent + "\nauthor Ada Author <Ada@Example.com> 1793440800 +0200\n" +
		"committer Olivia <olivia@example.com> 1793442600 -0530\ngpgsig " + strings.ReplaceAll(signature, "\n", "\n ") +
		"\n\nAdd README.md\n\nWith a body.\n"
	tests := []struct {
		name    string
		content string
		want    func(c git.Commit) bool
		wantErr string
	}{
		{name: "a root commit", content: "tree " + tree + "\nauthor A <a@x> 0 +0000\ncommitter A <a@x> 0 +0000\n\nFirst\n",
			want: func(c git.Commit) bool {
				return c.Tree == tree && len(c.Parents) == 0 && c.Parents != nil && c.Message == "First\n"
			}},
		{name: "a merge keeps its parents in order", content: "tree " + tree + "\nparent " + parent + "\nparent " + other +
			"\nauthor A <a@x> 0 +0000\ncommitter A <a@x> 0 +0000\n\nMerge\n",
			want: func(c git.Commit) bool { return len(c.Parents) == 2 && c.Parents[0] == parent && c.Parents[1] == other }},
		{name: "a signed commit", content: signed, want: func(c git.Commit) bool {
			return c.Signature == signature && c.Message == "Add README.md\n\nWith a body.\n" && c.Author.Name == "Ada Author" &&
				c.Author.Email == "Ada@Example.com" && c.Author.Zone == "+0200" && c.Author.At.Equal(time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)) &&
				c.Committer.Zone == "-0530" && c.Committer.At.Format("15:04 -0700") == "05:00 -0530"
		}},
		{name: "headers it keeps nothing of", content: "tree " + tree + "\nauthor A <a@x> 0 +0000\ncommitter A <a@x> 0 +0000\nencoding ISO-8859-1\n\nMsg\n",
			want: func(c git.Commit) bool { return c.Tree == tree && c.Message == "Msg\n" }},
		{name: "a commit without a message", content: "tree " + tree + "\nauthor A <a@x> 0 +0000\ncommitter A <a@x> 0 +0000",
			want: func(c git.Commit) bool { return c.Message == "" && c.Committer.Email == "a@x" }},
		{name: "no tree", content: "author A <a@x> 0 +0000\n\nMsg\n", wantErr: "names no tree"},
		{name: "an author without an email", content: "tree " + tree + "\nauthor A 0 +0000\n\nMsg\n", wantErr: "reading its author"},
		{name: "an author without a time", content: "tree " + tree + "\nauthor A <a@x> soon +0000\n\nMsg\n", wantErr: `"soon" is no time`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := parseCommit("c0ffee", []byte(tt.content))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseCommit() = %+v, %v, want an error saying %q", c, err, tt.wantErr)
				}
				return
			}
			if err != nil || c.SHA != "c0ffee" || !tt.want(c) {
				t.Errorf("parseCommit() = %+v, %v", c, err)
			}
		})
	}
}

func TestParsePerson(t *testing.T) {
	tests := []struct {
		value, name, email, zone string
		offset                   int
	}{
		{value: "Ada Author <ada@example.com> 0 +0200", name: "Ada Author", email: "ada@example.com", zone: "+0200", offset: 7200},
		{value: "<anonymous@example.com> 0 -0130", email: "anonymous@example.com", zone: "-0130", offset: -5400},
		{value: "Odd <odd@example.com> 0 local", name: "Odd", email: "odd@example.com", zone: "local"},
		{value: "No email <> 0 +0000", name: "No email", zone: "+0000"},
	}
	for _, tt := range tests {
		p, err := parsePerson(tt.value)
		if err != nil {
			t.Errorf("parsePerson(%q): %v", tt.value, err)
			continue
		}
		if _, offset := p.At.Zone(); p.Name != tt.name || p.Email != tt.email || p.Zone != tt.zone || offset != tt.offset {
			t.Errorf("parsePerson(%q) = %+v at offset %d, want %q <%s> %s at %d", tt.value, p, offset, tt.name, tt.email, tt.zone, tt.offset)
		}
	}
}

func TestReadObjects(t *testing.T) {
	type object struct{ sha, kind, content string }
	tests := []struct {
		name    string
		out     string
		want    []object
		wantErr bool
	}{
		{name: "two objects", out: "a commit 5\nhello\nb blob 0\n\n", want: []object{{"a", "commit", "hello"}, {"b", "blob", ""}}},
		{name: "content with newlines", out: "a commit 3\na\nb\n", want: []object{{"a", "commit", "a\nb"}}},
		{name: "nothing", out: ""},
		{name: "a missing object", out: "deadbeef missing\n", wantErr: true},
		{name: "a short object", out: "a commit 10\nhi\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []object
			err := readObjects(strings.NewReader(tt.out), func(sha, kind string, content []byte) error {
				got = append(got, object{sha, kind, string(content)})
				return nil
			})
			if (err != nil) != tt.wantErr || len(got) != len(tt.want) {
				t.Fatalf("readObjects() read %+v, %v", got, err)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("object %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
