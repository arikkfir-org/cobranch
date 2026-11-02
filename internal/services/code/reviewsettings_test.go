package code_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
)

// reviewSettingsCase is a /cobranch-review.yaml and what it says, or why it doesn't parse.
type reviewSettingsCase struct {
	name     string
	settings string
	want     git.ReviewSettings
	wantErr  string
}

// reviewSettingsCases are TestParseReviewSettings's cases.
func reviewSettingsCases() []reviewSettingsCase {
	var many []string
	for i := range code.MaxReviewIncludes + 1 {
		many = append(many, fmt.Sprintf("docs/%d.md", i))
	}
	defaults := git.DefaultReviewSettings()
	with := func(change func(*git.ReviewSettings)) git.ReviewSettings {
		s := git.DefaultReviewSettings()
		change(&s)
		return s
	}
	return []reviewSettingsCase{
		{name: "an empty file", settings: "", want: defaults},
		{name: "only comments", settings: "# the defaults\n", want: defaults},
		{name: "every key null", settings: "enabled:\nbranches:\npaths:\ninstructions:\ninclude:\nmaxDiffLines:\n", want: defaults},
		{name: "the design's example", settings: "enabled: true\nbranches: [\"**\"]\npaths:\n  ignore: [\"**/*.gen.go\", \"vendor/**\", \"**/*.lock\"]\n" +
			"instructions: |\n  Handlers answer errors as RFC 9457 problems.\ninclude: [CONTRIBUTING.md, \"docs/conventions/*.md\"]\nmaxDiffLines: 5000\n",
			want: git.ReviewSettings{Enabled: true, Branches: []string{"**"}, Ignore: []string{"**/*.gen.go", "vendor/**", "**/*.lock"},
				Instructions: "Handlers answer errors as RFC 9457 problems.\n", Include: []string{"CONTRIBUTING.md", "docs/conventions/*.md"}, MaxDiffLines: 5000}},
		{name: "disabled", settings: "enabled: false\n", want: with(func(s *git.ReviewSettings) { s.Enabled = false })},
		{name: "some branches", settings: "branches: [feature/*, fix/**]\n", want: with(func(s *git.ReviewSettings) { s.Branches = []string{"feature/*", "fix/**"} })},
		{name: "paths of nothing", settings: "paths:\n", want: defaults},
		{name: "the most diff lines", settings: "maxDiffLines: 20000\n", want: with(func(s *git.ReviewSettings) { s.MaxDiffLines = 20000 })},
		{name: "the longest instructions", settings: "instructions: " + strings.Repeat("é", code.MaxReviewInstructions) + "\n",
			want: with(func(s *git.ReviewSettings) { s.Instructions = strings.Repeat("é", code.MaxReviewInstructions) })},
		{name: "not a mapping", settings: "- main\n", wantErr: "/cobranch-review.yaml: line 1: the settings must be a mapping"},
		{name: "a syntax error", settings: "enabled: [true\n", wantErr: "/cobranch-review.yaml: line 1: did not find expected ',' or ']'"},
		{name: "an unknown key", settings: "model: gpt\n", wantErr: `/cobranch-review.yaml: line 1: the settings takes no "model"`},
		{name: "a key named twice", settings: "enabled: true\nenabled: false\n", wantErr: `/cobranch-review.yaml: line 2: the settings names "enabled" twice`},
		{name: "enabled as text", settings: "enabled: yes\n", wantErr: "/cobranch-review.yaml: line 1: enabled must be true or false"},
		{name: "branches as text", settings: "branches: main\n", wantErr: "/cobranch-review.yaml: line 1: branches must be a list of names or patterns such as release/*"},
		{name: "no branches", settings: "branches: []\n", wantErr: "/cobranch-review.yaml: line 1: branches must be a list of names or patterns such as release/*"},
		{name: "a branch twice", settings: "branches: [a, a]\n", wantErr: `/cobranch-review.yaml: line 1: branches names "a" twice`},
		{name: "an unknown key of paths", settings: "paths:\n  only: [src/**]\n", wantErr: `/cobranch-review.yaml: line 2: paths takes no "only"`},
		{name: "paths as a list", settings: "paths: [vendor/**]\n", wantErr: "/cobranch-review.yaml: line 1: paths must be a mapping"},
		{name: "an absolute path ignored", settings: "paths:\n  ignore: [/vendor/**]\n",
			wantErr: "/cobranch-review.yaml: line 2: ignore must be a list of paths from the root, or their patterns such as docs/**/*.md"},
		{name: "a path included twice", settings: "include: [README.md, README.md]\n", wantErr: `/cobranch-review.yaml: line 1: include names "README.md" twice`},
		{name: "too many included", settings: "include: [" + strings.Join(many, ", ") + "]\n",
			wantErr: "/cobranch-review.yaml: line 1: include names at most 20 paths or patterns"},
		{name: "instructions as a list", settings: "instructions: [be kind]\n", wantErr: "/cobranch-review.yaml: line 1: instructions must be text"},
		{name: "instructions too long", settings: "instructions: " + strings.Repeat("a", code.MaxReviewInstructions+1) + "\n",
			wantErr: "/cobranch-review.yaml: line 1: instructions must be at most 20000 characters"},
		{name: "no diff lines", settings: "maxDiffLines: 0\n", wantErr: "/cobranch-review.yaml: line 1: maxDiffLines must be a whole number from 1 to 20000"},
		{name: "too many diff lines", settings: "maxDiffLines: 20001\n", wantErr: "/cobranch-review.yaml: line 1: maxDiffLines must be a whole number from 1 to 20000"},
		{name: "diff lines as text", settings: "maxDiffLines: \"5000\"\n", wantErr: "/cobranch-review.yaml: line 1: maxDiffLines must be a whole number from 1 to 20000"},
	}
}

func TestParseReviewSettings(t *testing.T) {
	tests := reviewSettingsCases()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := code.ParseReviewSettings([]byte(tt.settings))
			if tt.wantErr != "" {
				var bad *code.SettingsError
				if !errors.As(err, &bad) || err.Error() != tt.wantErr || bad.File != git.ReviewSettingsFile {
					t.Fatalf("ParseReviewSettings() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseReviewSettings(): %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseReviewSettings() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
