package jobs_test

import (
	"testing"

	"cobranch.dev/internal/services/jobs"
)

func TestMatch(t *testing.T) {
	tests := []struct {
		pattern, name string
		want          bool
	}{
		{"main", "main", true},
		{"main", "mainline", false},
		{"release/*", "release/1.0", true},
		{"release/*", "release/1.0/rc", false},
		{"release/**", "release/1.0/rc", true},
		{"release/**", "release/", true},
		{"**", "any/thing/at/all", true},
		{"*", "feature/x", false},
		{"feature-?", "feature-a", true},
		{"feature-?", "feature-ab", false},
		{"feature-?", "feature-/", false},
		{"v?", "vé", true},
		{"v*.*", "v1.2", true},
		{"v*.*", "v1", false},
		{"**/fix", "a/b/fix", true},
		{"", "", true},
		{"", "main", false},
	}
	for _, tt := range tests {
		if got := jobs.Match(tt.pattern, tt.name); got != tt.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
		}
	}
}

func TestJobMatches(t *testing.T) {
	push := jobs.Job{Name: "build", On: jobs.Triggers{Push: &jobs.PushTrigger{Branches: []string{"main", "release/*"}}}}
	every := jobs.Job{Name: "build", On: jobs.Triggers{Push: &jobs.PushTrigger{}, Tag: &jobs.TagTrigger{}}}
	tags := jobs.Job{Name: "release", On: jobs.Triggers{Tag: &jobs.TagTrigger{Tags: []string{"v*"}}}}
	follow := jobs.Job{Name: "deploy", On: jobs.Triggers{Annotation: &jobs.AnnotationTrigger{Names: []string{"build", "deploy"},
		Statuses: []string{"successful"}, Branches: []string{"main"}}}}
	review := jobs.Job{Name: "merge", On: jobs.Triggers{Review: &jobs.ReviewTrigger{States: []string{"approved"}}}}
	posted := jobs.Job{Name: "deploy", On: jobs.Triggers{Event: &jobs.EventTrigger{Names: []string{"deploy/requested"}}}}
	nightly := jobs.Job{Name: "nightly", On: jobs.Triggers{Schedule: []jobs.Schedule{{Cron: "0 3 * * *"}}, Manual: &jobs.ManualTrigger{}}}
	tests := []struct {
		name  string
		job   jobs.Job
		event jobs.Event
		want  bool
	}{
		{"a push to a listed branch", push, jobs.Event{Kind: jobs.EventPush, Ref: "refs/heads/main"}, true},
		{"a push to a matching branch", push, jobs.Event{Kind: jobs.EventPush, Ref: "refs/heads/release/2"}, true},
		{"a push to another branch", push, jobs.Event{Kind: jobs.EventPush, Ref: "refs/heads/feature"}, false},
		{"a push to any branch", every, jobs.Event{Kind: jobs.EventPush, Ref: "refs/heads/feature"}, true},
		{"a tag isn't a push", push, jobs.Event{Kind: jobs.EventTag, Ref: "refs/tags/main"}, false},
		{"a push event of a tag", every, jobs.Event{Kind: jobs.EventPush, Ref: "refs/tags/v1"}, false},
		{"any tag", every, jobs.Event{Kind: jobs.EventTag, Ref: "refs/tags/anything"}, true},
		{"a matching tag", tags, jobs.Event{Kind: jobs.EventTag, Ref: "refs/tags/v1.2"}, true},
		{"another tag", tags, jobs.Event{Kind: jobs.EventTag, Ref: "refs/tags/nightly"}, false},
		{"an annotation it follows", follow, jobs.Event{Kind: jobs.EventAnnotation, Ref: "refs/heads/main", Name: "build", State: "successful"}, true},
		{"its own annotation", follow, jobs.Event{Kind: jobs.EventAnnotation, Ref: "refs/heads/main", Name: "deploy", State: "successful"}, false},
		{"another status", follow, jobs.Event{Kind: jobs.EventAnnotation, Ref: "refs/heads/main", Name: "build", State: "failed"}, false},
		{"another branch's annotation", follow, jobs.Event{Kind: jobs.EventAnnotation, Ref: "refs/heads/x", Name: "build", State: "successful"}, false},
		{"an approval", review, jobs.Event{Kind: jobs.EventReview, Ref: "refs/heads/x", State: "approved"}, true},
		{"changes requested", review, jobs.Event{Kind: jobs.EventReview, Ref: "refs/heads/x", State: "changes_requested"}, false},
		{"a posted event it follows", posted, jobs.Event{Kind: jobs.EventPosted, Ref: "refs/heads/main", Name: "deploy/requested"}, true},
		{"another posted event", posted, jobs.Event{Kind: jobs.EventPosted, Ref: "refs/heads/main", Name: "other"}, false},
		{"a schedule", nightly, jobs.Event{Kind: jobs.EventSchedule, Ref: "refs/heads/main"}, true},
		{"a manual start", nightly, jobs.Event{Kind: jobs.EventManual, Ref: "refs/heads/x"}, true},
		{"a manual start of a job that takes none", push, jobs.Event{Kind: jobs.EventManual, Ref: "refs/heads/main"}, false},
		{"a schedule of a job without one", push, jobs.Event{Kind: jobs.EventSchedule, Ref: "refs/heads/main"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.job.Matches(tt.event); got != tt.want {
				t.Errorf("Matches(%+v) = %v, want %v", tt.event, got, tt.want)
			}
		})
	}
}

func TestJobKind(t *testing.T) {
	tests := []struct {
		job  jobs.Job
		want jobs.Kind
	}{
		{jobs.Job{Script: "x"}, jobs.KindScript},
		{jobs.Job{Pod: []byte("{}")}, jobs.KindPod},
		{jobs.Job{Webhook: &jobs.Webhook{URL: "https://x"}}, jobs.KindWebhook},
	}
	for _, tt := range tests {
		if got := tt.job.Kind(); got != tt.want {
			t.Errorf("Kind() of %+v = %s, want %s", tt.job, got, tt.want)
		}
	}
}
