package code_test

import (
	"reflect"
	"testing"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
)

func TestEvaluate(t *testing.T) {
	zero, two := 0, 2
	build := func(s code.AnnotationStatus) []code.Annotation {
		return []code.Annotation{{Name: "ci/build", Status: s}}
	}
	requireBuild := git.Requirements{Branch: "main", Annotations: []string{"ci/build"}}
	linear := git.Requirements{Branch: "main", LinearHistory: true}
	upToDate := code.Check{Kind: code.CheckUpToDate, Met: true}
	tests := []struct {
		name       string
		in         code.Standing
		want       code.BranchStatus
		wantChecks []code.Check
	}{
		{name: "waiting for the push worker", in: code.Standing{Waiting: true, Facts: git.Facts{Ahead: 1}},
			want: code.StatusProcessing, wantChecks: []code.Check{upToDate}},
		{name: "a required annotation pending", in: code.Standing{Requirements: requireBuild, Facts: git.Facts{Ahead: 1}, Annotations: build(code.AnnotationPending)},
			want: code.StatusProcessing, wantChecks: []code.Check{{Kind: code.CheckAnnotation, Name: "ci/build", Status: code.AnnotationPending}, upToDate}},
		{name: "a required annotation in progress, even with nothing to merge", in: code.Standing{Requirements: requireBuild, Annotations: build(code.AnnotationInProgress)},
			want: code.StatusProcessing, wantChecks: []code.Check{{Kind: code.CheckAnnotation, Name: "ci/build", Status: code.AnnotationInProgress}, upToDate}},
		{name: "a required annotation missing", in: code.Standing{Requirements: requireBuild, Facts: git.Facts{Ahead: 1}},
			want: code.StatusBlocked, wantChecks: []code.Check{{Kind: code.CheckAnnotation, Name: "ci/build"}, upToDate}},
		{name: "a required annotation failed", in: code.Standing{Requirements: requireBuild, Facts: git.Facts{Ahead: 1}, Annotations: build(code.AnnotationFailed)},
			want: code.StatusBlocked, wantChecks: []code.Check{{Kind: code.CheckAnnotation, Name: "ci/build", Status: code.AnnotationFailed}, upToDate}},
		{name: "an annotation nothing requires, pending", in: code.Standing{Facts: git.Facts{Ahead: 1}, Annotations: build(code.AnnotationPending)},
			want: code.StatusReady, wantChecks: []code.Check{upToDate}},
		{name: "merges where linear history is required", in: code.Standing{Requirements: linear, Facts: git.Facts{Ahead: 3, Merges: &two}},
			want: code.StatusBlocked, wantChecks: []code.Check{{Kind: code.CheckLinearHistory, Count: 2}, upToDate}},
		{name: "merges nobody counted where linear history is required", in: code.Standing{Requirements: linear, Facts: git.Facts{Ahead: 1}},
			want: code.StatusBlocked, wantChecks: []code.Check{{Kind: code.CheckLinearHistory}, upToDate}},
		{name: "nothing to merge where linear history is required", in: code.Standing{Requirements: linear},
			want: code.StatusEmpty, wantChecks: []code.Check{{Kind: code.CheckLinearHistory, Met: true}, upToDate}},
		{name: "linear", in: code.Standing{Requirements: linear, Facts: git.Facts{Ahead: 1, Merges: &zero}},
			want: code.StatusReady, wantChecks: []code.Check{{Kind: code.CheckLinearHistory, Met: true}, upToDate}},
		{name: "behind its base", in: code.Standing{Facts: git.Facts{Ahead: 1, Behind: 4}},
			want: code.StatusBlocked, wantChecks: []code.Check{{Kind: code.CheckUpToDate, Count: 4}}},
		{name: "behind with nothing to merge", in: code.Standing{Requirements: requireBuild, Facts: git.Facts{Behind: 4}},
			want: code.StatusEmpty, wantChecks: []code.Check{{Kind: code.CheckAnnotation, Name: "ci/build"}, {Kind: code.CheckUpToDate, Count: 4}}},
		{name: "merged", in: code.Standing{Facts: git.Facts{Behind: 2}, Merged: true},
			want: code.StatusMerged, wantChecks: []code.Check{{Kind: code.CheckUpToDate, Count: 2}}},
		{name: "ready", in: code.Standing{Requirements: requireBuild, Facts: git.Facts{Ahead: 2}, Annotations: build(code.AnnotationSuccessful)},
			want: code.StatusReady, wantChecks: []code.Check{{Kind: code.CheckAnnotation, Name: "ci/build", Status: code.AnnotationSuccessful, Met: true}, upToDate}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, checks := code.Evaluate(tt.in)
			if got != tt.want {
				t.Errorf("Evaluate() = %s, want %s", got, tt.want)
			}
			if !reflect.DeepEqual(checks, tt.wantChecks) {
				t.Errorf("checks = %+v, want %+v", checks, tt.wantChecks)
			}
		})
	}
}
