package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"cobranch.dev/internal/services/harness"
)

// workspace is a Workspace over a directory of a few files, a link out of it, and a .git directory.
func workspace(t *testing.T) *Workspace {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"README.md":         "# Site\n\nSweep the floor.\n",
		"src/main.go":       "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n",
		"src/deep/a/b/c.go": "package b\n",
		".git/HEAD":         "ref: refs/heads/main\n",
		"logo.png":          "\x89PNG\x00\x00",
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc", filepath.Join(dir, "etc")); err != nil {
		t.Fatal(err)
	}
	w, err := New(dir, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

// toolCase is a call of one of the workspace's tools, with what it answers, or the failure it starts with.
type toolCase struct {
	name, tool, args string
	want             string
	wantErr          string
	// file and wantFile check what a file holds after the call.
	file, wantFile string
}

func TestTools(t *testing.T) {
	tt := []toolCase{
		{name: "list the workspace", tool: "list_files", args: `{}`,
			want: ".git/\nREADME.md\netc\nlogo.png\nsrc/\nsrc/deep/\nsrc/main.go"},
		{name: "list deeper", tool: "list_files", args: `{"path":"src","depth":9}`, want: "deep/\ndeep/a/\ndeep/a/b/\ndeep/a/b/c.go\nmain.go"},
		{name: "list outside", tool: "list_files", args: `{"path":"/etc"}`, wantErr: "/etc is outside the workspace"},
		{name: "list through a link out", tool: "list_files", args: `{"path":"etc"}`, wantErr: "listing etc"},
		{name: "unknown arguments", tool: "list_files", args: `{"dir":"src"}`, wantErr: `reading the arguments: json: unknown field "dir"`},
		{name: "read", tool: "read_file", args: `{"path":"src/main.go","offset":3,"limit":2}`,
			want: "     3\tfunc main() {\n     4\t\tprintln(\"hi\")\n[1 more lines: read on from line 5]\n"},
		{name: "read an absolute path", tool: "read_file", args: `{"path":"WORKSPACE/README.md"}`, want: "     1\t# Site\n     2\t\n     3\tSweep the floor.\n"},
		{name: "read past the end", tool: "read_file", args: `{"path":"README.md","offset":9}`, wantErr: "README.md has 3 lines"},
		{name: "read a binary", tool: "read_file", args: `{"path":"logo.png"}`, wantErr: "logo.png is binary, not text"},
		{name: "read through a link out", tool: "read_file", args: `{"path":"etc/hostname"}`, wantErr: "reading etc/hostname"},
		{name: "search", tool: "search", args: `{"pattern":"^package|floor"}`,
			want: "README.md:3: Sweep the floor.\nsrc/deep/a/b/c.go:1: package b\nsrc/main.go:1: package main"},
		{name: "search a glob", tool: "search", args: `{"pattern":"package","path":"src","glob":"main.*"}`, want: "src/main.go:1: package main"},
		{name: "search without a match", tool: "search", args: `{"pattern":"mop"}`, want: "No line matches."},
		{name: "search a bad pattern", tool: "search", args: `{"pattern":"("}`, wantErr: "reading the pattern"},
		{name: "write", tool: "write_file", args: `{"path":"docs/new/guide.md","content":"Mop."}`, want: "Wrote 4 bytes to docs/new/guide.md.",
			file: "docs/new/guide.md", wantFile: "Mop."},
		{name: "write outside", tool: "write_file", args: `{"path":"../escape","content":"x"}`, wantErr: "../escape is outside the workspace"},
		{name: "edit", tool: "edit_file", args: `{"path":"README.md","old":"Sweep","new":"Mop"}`, want: "Replaced 1 of old in README.md.",
			file: "README.md", wantFile: "# Site\n\nMop the floor.\n"},
		{name: "edit every one", tool: "edit_file", args: `{"path":"src/main.go","old":"main","new":"app","all":true}`, want: "Replaced 2 of old in src/main.go.",
			file: "src/main.go", wantFile: "package app\n\nfunc app() {\n\tprintln(\"hi\")\n}\n"},
		{name: "edit one of many", tool: "edit_file", args: `{"path":"src/main.go","old":"main","new":"app"}`, wantErr: "src/main.go holds old 2 times"},
		{name: "edit what isn't there", tool: "edit_file", args: `{"path":"README.md","old":"mop","new":"x"}`, wantErr: "README.md doesn't hold old"},
		{name: "a command", tool: "shell", args: `{"command":"cat README.md | wc -l; ls src"}`, want: "3\ndeep\nmain.go\n"},
		{name: "a command that fails", tool: "shell", args: `{"command":"echo half; exit 3"}`, want: "half\n", wantErr: "the command exited with status 3"},
		{name: "a command without Cobranch's variables", tool: "shell", args: `{"command":"echo \"[$COBRANCH_TOKEN_FILE]\""}`, want: "[]\n"},
		{name: "a command that runs too long", tool: "shell", args: `{"command":"sleep 30 & sleep 30","timeout":1}`, wantErr: "the command ran out of its 1s"},
	}
	t.Setenv("COBRANCH_TOKEN_FILE", "/var/run/cobranch/token")
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) { runTool(t, tc) })
	}
}

func TestLargeFiles(t *testing.T) {
	// A file of any size costs read_file only the lines it shows, each cut; edit_file, which reads a file whole, refuses
	// one past its limit.
	w := workspace(t)
	var big strings.Builder
	for i := 1; big.Len() <= maxEdited; i++ {
		big.WriteString("line " + strconv.Itoa(i) + "\n")
	}
	files := map[string]string{"big.log": big.String(), "long.txt": strings.Repeat("é", maxLine) + "\nend"}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(w.dir, name), []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	lines := strings.Count(big.String(), "\n")
	tt := []toolCase{
		{name: "read a window of a large file", tool: "read_file", args: `{"path":"big.log","offset":500000,"limit":2}`,
			want: "500000\tline 500000\n500001\tline 500001\n[" + strconv.Itoa(lines-500001) + " more lines: read on from line 500002]\n"},
		{name: "read past the end of a large file", tool: "read_file", args: `{"path":"big.log","offset":` + strconv.Itoa(lines+1) + `}`,
			wantErr: "big.log has " + strconv.Itoa(lines) + " lines"},
		{name: "read a long line", tool: "read_file", args: `{"path":"long.txt"}`,
			want: "     1\t" + strings.Repeat("é", maxLine/2) + "…\n     2\tend\n"},
		{name: "edit a large file", tool: "edit_file", args: `{"path":"big.log","old":"line 1\n","new":""}`,
			wantErr: "big.log holds " + strconv.Itoa(big.Len()) + " bytes, over the 8 MiB edit_file takes"},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			got, err := run(t, w, tc.tool, tc.args)
			if (tc.wantErr == "" && err != nil) || (tc.wantErr != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.wantErr))) {
				t.Fatalf("%s(%s) = %.200q, %v; want the error %q", tc.tool, tc.args, got, err, tc.wantErr)
			}
			if tc.wantErr == "" && got != tc.want {
				t.Errorf("%s(%s) = %.200q, want %.200q", tc.tool, tc.args, got, tc.want)
			}
		})
	}
}

// runTool calls tc's tool in a workspace of its own, and checks what it answers and leaves.
func runTool(t *testing.T, tc toolCase) {
	t.Helper()
	w := workspace(t)
	got, err := run(t, w, tc.tool, strings.ReplaceAll(tc.args, "WORKSPACE", w.dir))
	if (tc.wantErr == "" && err != nil) || (tc.wantErr != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.wantErr))) {
		t.Fatalf("%s(%s) = %q, %v; want the error %q", tc.tool, tc.args, got, err, tc.wantErr)
	}
	if (tc.want != "" || tc.wantErr == "") && got != tc.want {
		t.Errorf("%s(%s) = %q, want %q", tc.tool, tc.args, got, tc.want)
	}
	if tc.file != "" {
		if data, err := os.ReadFile(filepath.Join(w.dir, tc.file)); err != nil || string(data) != tc.wantFile {
			t.Errorf("%s holds %q, %v; want %q", tc.file, data, err, tc.wantFile)
		}
	}
}

// run calls the workspace's tool name with args.
func run(t *testing.T, w *Workspace, name, args string) (string, error) {
	t.Helper()
	for _, tool := range w.Tools() {
		if tool.Spec().Name == name {
			out, err := tool.Run(context.Background(), harness.Call{ID: "c", Name: name, Arguments: json.RawMessage(args)})
			return out, err //nolint:wrapcheck // the test checks the tool's own error
		}
	}
	t.Fatalf("no tool %s", name)
	return "", nil
}

func TestSpecs(t *testing.T) {
	w := workspace(t)
	for _, tool := range w.Tools() {
		s := tool.Spec()
		var schema struct {
			Type       string                     `json:"type"`
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(s.Parameters, &schema); err != nil || schema.Type != "object" || len(schema.Properties) == 0 || schema.Required == nil {
			t.Errorf("%s's parameters = %s, %v", s.Name, s.Parameters, err)
		}
		for _, r := range schema.Required {
			if _, ok := schema.Properties[r]; !ok {
				t.Errorf("%s requires %s, which it doesn't describe", s.Name, r)
			}
		}
	}
}

func TestLongOutputKeepsItsEnds(t *testing.T) {
	var out headTail
	for i := range 20000 {
		_, _ = out.Write([]byte(strings.Repeat("x", 9) + string(rune('a'+i%26)))) // a headTail never fails
	}
	got := out.String()
	if len(got) > harness.MaxOutput || !strings.HasPrefix(got, "xxxxxxxxxa") || !strings.HasSuffix(got, "xxxxxxxxxx"[:9]+string(rune('a'+19999%26))) ||
		!strings.Contains(got, "bytes left out]") {
		t.Errorf("String() = %d bytes, %q…%q", len(got), got[:20], got[len(got)-20:])
	}
}
