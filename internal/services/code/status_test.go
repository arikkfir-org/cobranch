package code_test

import (
	"reflect"
	"testing"

	"github.com/google/uuid"

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

func TestEvaluateReviews(t *testing.T) {
	alice, bob, carol, agent := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	by := func(id uuid.UUID, name string, state code.ReviewState) code.Review {
		return code.Review{ID: uuid.New(), State: state, Reviewer: &code.Principal{ID: id, DisplayName: name}, Person: id != agent}
	}
	two := git.Requirements{Branch: "main", Reviews: &git.ReviewRequirements{Approvals: 2}}
	none := git.Requirements{Branch: "main", Reviews: &git.ReviewRequirements{}}
	upToDate := code.Check{Kind: code.CheckUpToDate, Met: true}
	approvals := func(n int) code.Check {
		return code.Check{Kind: code.CheckApprovals, Count: n, Required: 2, Met: n >= 2}
	}
	tests := []struct {
		name       string
		in         code.Standing
		want       code.BranchStatus
		wantChecks []code.Check
	}{
		{name: "no reviews where two approvals are required", in: code.Standing{Requirements: two, Facts: git.Facts{Ahead: 1}},
			want: code.StatusBlocked, wantChecks: []code.Check{approvals(0), upToDate}},
		{name: "two people approve", in: code.Standing{Requirements: two, Facts: git.Facts{Ahead: 1},
			Reviews: []code.Review{by(alice, "Alice", code.ReviewApproved), by(bob, "Bob", code.ReviewApproved)}},
			want: code.StatusReady, wantChecks: []code.Check{approvals(2), upToDate}},
		{name: "the author's and the pusher's approvals don't count", in: code.Standing{Requirements: two, Facts: git.Facts{Ahead: 1},
			Reviews: []code.Review{by(alice, "Alice", code.ReviewApproved), by(bob, "Bob", code.ReviewApproved), by(carol, "Carol", code.ReviewApproved)},
			Authors: []uuid.UUID{alice, bob}},
			want: code.StatusBlocked, wantChecks: []code.Check{approvals(1), upToDate}},
		{name: "an agent's approval doesn't count", in: code.Standing{Requirements: two, Facts: git.Facts{Ahead: 1},
			Reviews: []code.Review{by(alice, "Alice", code.ReviewApproved), by(agent, "AI reviewer", code.ReviewApproved)}},
			want: code.StatusBlocked, wantChecks: []code.Check{approvals(1), upToDate}},
		{name: "a reviewer's latest review counts", in: code.Standing{Requirements: two, Facts: git.Facts{Ahead: 1},
			Reviews: []code.Review{by(alice, "Alice", code.ReviewApproved), by(bob, "Bob", code.ReviewApproved), by(alice, "Alice", code.ReviewNeutral)}},
			want: code.StatusBlocked, wantChecks: []code.Check{approvals(1), upToDate}},
		{name: "a gone reviewer's approval doesn't count", in: code.Standing{Requirements: two, Facts: git.Facts{Ahead: 1},
			Reviews: []code.Review{by(alice, "Alice", code.ReviewApproved), {ID: uuid.New(), State: code.ReviewApproved, Person: true}}},
			want: code.StatusBlocked, wantChecks: []code.Check{approvals(1), upToDate}},
		{name: "changes requested by anyone block, an agent's too", in: code.Standing{Requirements: none, Facts: git.Facts{Ahead: 1},
			Reviews: []code.Review{by(agent, "AI reviewer", code.ReviewChangesRequested), by(alice, "Alice", code.ReviewChangesRequested)}},
			want: code.StatusBlocked, wantChecks: []code.Check{{Kind: code.CheckChangesRequested, Name: "AI reviewer"},
				{Kind: code.CheckChangesRequested, Name: "Alice"}, upToDate}},
		{name: "changes requested, then approved", in: code.Standing{Requirements: two, Facts: git.Facts{Ahead: 1},
			Reviews: []code.Review{by(alice, "Alice", code.ReviewChangesRequested), by(bob, "Bob", code.ReviewApproved), by(alice, "Alice", code.ReviewApproved)}},
			want: code.StatusReady, wantChecks: []code.Check{approvals(2), upToDate}},
		{name: "reviews with no approvals required and none requesting changes", in: code.Standing{Requirements: none, Facts: git.Facts{Ahead: 1},
			Reviews: []code.Review{by(alice, "Alice", code.ReviewNeutral)}},
			want: code.StatusReady, wantChecks: []code.Check{upToDate}},
		{name: "changes requested where the base requires no reviews", in: code.Standing{Requirements: git.Requirements{Branch: "main"}, Facts: git.Facts{Ahead: 1},
			Reviews: []code.Review{by(alice, "Alice", code.ReviewChangesRequested)}},
			want: code.StatusReady, wantChecks: []code.Check{upToDate}},
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
