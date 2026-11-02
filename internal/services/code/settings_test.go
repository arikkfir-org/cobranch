package code_test

import (
	"errors"
	"reflect"
	"testing"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
)

func TestParseSettings(t *testing.T) {
	tests := []struct {
		name     string
		settings string
		want     []git.Requirements
		wantErr  string
	}{
		{name: "an empty file", settings: ""},
		{name: "only comments", settings: "# nothing yet\n"},
		{name: "no branches", settings: "jobs:\n  build: {}\n"},
		{name: "branches of none", settings: "branches:\n"},
		{name: "the design's example", settings: "branches:\n  main:\n    requires:\n      annotations: [ci/build, ci/test]\n      linearHistory: true\n",
			want: []git.Requirements{{Branch: "main", Annotations: []string{"ci/build", "ci/test"}, LinearHistory: true}}},
		{name: "a branch named with nothing required", settings: "branches:\n  release:\n  main:\n    requires: {}\n",
			want: []git.Requirements{{Branch: "release"}, {Branch: "main"}}},
		{name: "linear history alone", settings: "branches:\n  main:\n    requires:\n      linearHistory: false\n",
			want: []git.Requirements{{Branch: "main"}}},
		{name: "keys for others left alone", settings: "jobs: [whatever]\nbranches:\n  main:\n    requires:\n      annotations: [ai-review]\n",
			want: []git.Requirements{{Branch: "main", Annotations: []string{"ai-review"}}}},
		{name: "not a mapping", settings: "- main\n", wantErr: "/cobranch.yaml: line 1: the settings must be a mapping"},
		{name: "branches as a list", settings: "branches: [main]\n", wantErr: "/cobranch.yaml: line 1: branches must map branch names to their settings"},
		{name: "a branch's settings as a list", settings: "branches:\n  main: [ci/build]\n", wantErr: "/cobranch.yaml: line 2: main's settings must be a mapping"},
		{name: "an unknown setting", settings: "branches:\n  main:\n    protected: true\n", wantErr: `/cobranch.yaml: line 3: main's settings takes no "protected"`},
		{name: "a misspelt requirement", settings: "branches:\n  main:\n    requires:\n      annotation: [ci/build]\n", wantErr: `/cobranch.yaml: line 4: requires takes no "annotation"`},
		{name: "annotations as a name", settings: "branches:\n  main:\n    requires:\n      annotations: ci/build\n", wantErr: "/cobranch.yaml: line 4: annotations must be a list of names"},
		{name: "an annotation's bad name", settings: "branches:\n  main:\n    requires:\n      annotations: [CI/Build]\n",
			wantErr: "/cobranch.yaml: line 4: annotations must be a list of names: 1 to 100 lowercase letters, digits, '-', '_', '.' and '/', starting with a letter or digit"},
		{name: "a number for a name", settings: "branches:\n  main:\n    requires:\n      annotations: [42]\n",
			wantErr: "/cobranch.yaml: line 4: annotations must be a list of names: 1 to 100 lowercase letters, digits, '-', '_', '.' and '/', starting with a letter or digit"},
		{name: "an annotation twice", settings: "branches:\n  main:\n    requires:\n      annotations:\n        - ci/build\n        - ci/build\n",
			wantErr: "/cobranch.yaml: line 6: annotation ci/build is required twice"},
		{name: "linear history as a string", settings: "branches:\n  main:\n    requires:\n      linearHistory: \"yes\"\n", wantErr: "/cobranch.yaml: line 4: linearHistory must be true or false"},
		{name: "a branch twice", settings: "branches:\n  main:\n  main:\n", wantErr: `/cobranch.yaml: line 3: branches names "main" twice`},
		{name: "branches twice", settings: "branches:\nbranches:\n", wantErr: `/cobranch.yaml: line 2: the settings names "branches" twice`},
		{name: "not YAML", settings: "branches: [\n", wantErr: "/cobranch.yaml: line 1: did not find expected node content"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { checkSettings(t, tt.settings, tt.want, tt.wantErr) })
	}
}

func TestParseReviewRequirements(t *testing.T) {
	tests := []struct {
		name     string
		settings string
		want     []git.Requirements
		wantErr  string
	}{
		{name: "two approvals", settings: "branches:\n  main:\n    requires:\n      reviews:\n        approvals: 2\n",
			want: []git.Requirements{{Branch: "main", Reviews: &git.ReviewRequirements{Approvals: 2}}}},
		{name: "reviews with no approvals", settings: "branches:\n  main:\n    requires:\n      reviews: {}\n",
			want: []git.Requirements{{Branch: "main", Reviews: &git.ReviewRequirements{}}}},
		{name: "reviews of null", settings: "branches:\n  main:\n    requires:\n      reviews:\n",
			want: []git.Requirements{{Branch: "main"}}},
		{name: "reviews as a number", settings: "branches:\n  main:\n    requires:\n      reviews: 2\n", wantErr: "/cobranch.yaml: line 4: reviews must be a mapping"},
		{name: "a misspelt review requirement", settings: "branches:\n  main:\n    requires:\n      reviews: {approval: 1}\n",
			wantErr: `/cobranch.yaml: line 4: reviews takes no "approval"`},
		{name: "too many approvals", settings: "branches:\n  main:\n    requires:\n      reviews: {approvals: 11}\n",
			wantErr: "/cobranch.yaml: line 4: approvals must be a whole number from 0 to 10"},
		{name: "negative approvals", settings: "branches:\n  main:\n    requires:\n      reviews: {approvals: -1}\n",
			wantErr: "/cobranch.yaml: line 4: approvals must be a whole number from 0 to 10"},
		{name: "approvals as a string", settings: "branches:\n  main:\n    requires:\n      reviews: {approvals: \"2\"}\n",
			wantErr: "/cobranch.yaml: line 4: approvals must be a whole number from 0 to 10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { checkSettings(t, tt.settings, tt.want, tt.wantErr) })
	}
}

// checkSettings checks that ParseSettings reads settings as want, or refuses them with a *SettingsError saying wantErr.
func checkSettings(t *testing.T, settings string, want []git.Requirements, wantErr string) {
	t.Helper()
	got, err := code.ParseSettings([]byte(settings))
	if wantErr != "" {
		var bad *code.SettingsError
		if !errors.As(err, &bad) || err.Error() != wantErr {
			t.Fatalf("ParseSettings() = %v, want *SettingsError %q", err, wantErr)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseSettings() = %+v, want %+v", got, want)
	}
}
