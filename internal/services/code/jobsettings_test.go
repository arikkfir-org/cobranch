package code_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/jobs"
)

const secretID = "0193a8c2-5c1e-7f20-9d4b-3e1f6a2b7c90"

// parseCase is a /cobranch.yaml and what ParseSettings makes of it: its repository settings and jobs, or the
// *SettingsError it is refused with.
type parseCase struct {
	name       string
	settings   string
	repository jobs.RepositorySettings
	jobs       []jobs.Job
	wantErr    string
}

// buildJob is ci/build, a script on every push, as change leaves it.
func buildJob(change func(j *jobs.Job)) jobs.Job {
	j := jobs.Job{Name: "ci/build", On: jobs.Triggers{Push: &jobs.PushTrigger{}}, Script: "make build",
		Permissions: []string{"annotations.write", "repo.read"}, Checkout: true}
	if change != nil {
		change(&j)
	}
	return j
}

func runParseCases(t *testing.T, tests []parseCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := code.ParseSettings([]byte(tt.settings))
			if tt.wantErr != "" {
				var bad *code.SettingsError
				if !errors.As(err, &bad) || err.Error() != tt.wantErr {
					t.Fatalf("ParseSettings() = %v, want *SettingsError %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Repository, tt.repository) {
				t.Errorf("ParseSettings().Repository = %+v, want %+v", got.Repository, tt.repository)
			}
			if !reflect.DeepEqual(got.Jobs, tt.jobs) {
				t.Errorf("ParseSettings().Jobs = %+v, want %+v", got.Jobs, tt.jobs)
			}
		})
	}
}

func TestParseSettingsJobs(t *testing.T) {
	defaults := jobs.DefaultRepositorySettings
	runParseCases(t, []parseCase{
		{name: "none", settings: "", repository: defaults, jobs: []jobs.Job{}},
		{name: "a script on every push", settings: "jobs:\n  ci/build:\n    on:\n      push: {}\n    script: make build\n",
			repository: defaults, jobs: []jobs.Job{buildJob(nil)}},
		{name: "by name", settings: "jobs:\n  z:\n    on: {manual: }\n    script: z\n  a:\n    on: {manual: }\n    script: a\n", repository: defaults,
			jobs: []jobs.Job{
				{Name: "a", On: jobs.Triggers{Manual: &jobs.ManualTrigger{}}, Script: "a", Permissions: jobs.DefaultPermissions, Checkout: true},
				{Name: "z", On: jobs.Triggers{Manual: &jobs.ManualTrigger{}}, Script: "z", Permissions: jobs.DefaultPermissions, Checkout: true},
			}},
		{name: "every setting of a script", repository: defaults,
			settings: "jobs:\n  ci/build:\n    on:\n      push:\n        branches: [main, \"release/**\"]\n      tag: {tags: [\"v*\"]}\n" +
				"    script: make build\n    image: golang:1.27\n    secrets:\n      API_TOKEN: " + secretID + "\n" +
				"    permissions: [repo.read, reviews.submit]\n    timeout: 30m\n    checkout: false\n",
			jobs: []jobs.Job{buildJob(func(j *jobs.Job) {
				j.On = jobs.Triggers{Push: &jobs.PushTrigger{Branches: []string{"main", "release/**"}}, Tag: &jobs.TagTrigger{Tags: []string{"v*"}}}
				j.Image, j.Secrets = "golang:1.27", map[string]uuid.UUID{"API_TOKEN": uuid.MustParse(secretID)}
				j.Permissions, j.Timeout, j.Checkout = []string{"repo.read", "reviews.submit"}, jobs.Duration(30*time.Minute), false
			})}},
		{name: "a pod", repository: defaults,
			settings: "jobs:\n  ci/test:\n    on: {push: }\n    pod:\n      containers:\n        - name: test\n          image: golang:1.27\n",
			jobs: []jobs.Job{{Name: "ci/test", On: jobs.Triggers{Push: &jobs.PushTrigger{}}, Permissions: jobs.DefaultPermissions, Checkout: true,
				Pod: json.RawMessage(`{"containers":[{"image":"golang:1.27","name":"test"}]}`)}}},
	})
}

func TestParseSettingsTriggers(t *testing.T) {
	defaults := jobs.DefaultRepositorySettings
	runParseCases(t, []parseCase{
		{name: "a webhook on annotations", repository: defaults,
			settings: "jobs:\n  deploy:\n    on:\n      annotation:\n        names: [ci/build]\n        branches: [main]\n" +
				"    webhook:\n      url: https://deploy.example.com/hook\n      signingSecret: " + secretID + "\n",
			jobs: []jobs.Job{{Name: "deploy", Permissions: jobs.DefaultPermissions, Checkout: true,
				On:      jobs.Triggers{Annotation: &jobs.AnnotationTrigger{Names: []string{"ci/build"}, Statuses: []string{"successful"}, Branches: []string{"main"}}},
				Webhook: &jobs.Webhook{URL: "https://deploy.example.com/hook", SigningSecret: uuid.MustParse(secretID)}}}},
		{name: "reviews, events and schedules", repository: defaults,
			settings: "jobs:\n  nightly:\n    on:\n      review: {states: [changes_requested]}\n      event: {names: [deploy/requested]}\n" +
				"      schedule:\n        - cron: \"0 3 * * *\"\n    script: make nightly\n",
			jobs: []jobs.Job{{Name: "nightly", Script: "make nightly", Permissions: jobs.DefaultPermissions, Checkout: true,
				On: jobs.Triggers{Review: &jobs.ReviewTrigger{States: []string{"changes_requested"}}, Event: &jobs.EventTrigger{Names: []string{"deploy/requested"}},
					Schedule: []jobs.Schedule{{Cron: "0 3 * * *"}}}}}},
		{name: "a review approved by default", repository: defaults, settings: "jobs:\n  a:\n    on: {review: }\n    script: a\n",
			jobs: []jobs.Job{{Name: "a", Script: "a", Permissions: jobs.DefaultPermissions, Checkout: true,
				On: jobs.Triggers{Review: &jobs.ReviewTrigger{States: []string{"approved"}}}}}},
		{name: "the repository's settings", settings: "repository:\n  outsidePushes: run\n  jobs:\n    ceiling: Developer\n",
			repository: jobs.RepositorySettings{OutsidePushes: jobs.OutsideRun, Ceiling: "Developer"}, jobs: []jobs.Job{}},
	})
}

func TestParseSettingsRefused(t *testing.T) {
	runParseCases(t, []parseCase{
		{name: "outside pushes misspelt", settings: "repository:\n  outsidePushes: allow\n", wantErr: "/cobranch.yaml: line 2: outsidePushes must be wait or run"},
		{name: "a misplaced gate", settings: "repository:\n  jobs:\n    outsidePushes: run\n", wantErr: `/cobranch.yaml: line 3: repository.jobs takes no "outsidePushes"`},
		{name: "jobs as a list", settings: "jobs: [build]\n", wantErr: "/cobranch.yaml: line 1: jobs must map job names to their jobs"},
		{name: "a job's bad name", settings: "jobs:\n  CI:\n    on: {push: }\n    script: x\n",
			wantErr: "/cobranch.yaml: line 2: a job's name must be 1 to 100 lowercase letters, digits, '-', '_', '.' and '/', starting with a letter or digit"},
		{name: "a delivery's name", settings: "jobs:\n  delivery/prod:\n    on: {push: }\n    script: x\n",
			wantErr: "/cobranch.yaml: line 2: a job's name can't start with delivery/, which deliveries report"},
		{name: "a job of nothing", settings: "jobs:\n  a:\n", wantErr: "/cobranch.yaml: line 2: job a needs on and one of pod, script or webhook"},
		{name: "no trigger", settings: "jobs:\n  a:\n    script: x\n", wantErr: "/cobranch.yaml: line 3: job a needs on, naming what triggers it"},
		{name: "an empty on", settings: "jobs:\n  a:\n    on: {}\n    script: x\n", wantErr: "/cobranch.yaml: line 3: job a needs a trigger under on"},
		{name: "a misspelt trigger", settings: "jobs:\n  a:\n    on: {pushed: }\n    script: x\n", wantErr: `/cobranch.yaml: line 3: on takes no "pushed"`},
		{name: "no work", settings: "jobs:\n  a:\n    on: {push: }\n", wantErr: "/cobranch.yaml: line 3: job a needs exactly one of pod, script or webhook"},
		{name: "two kinds of work", settings: "jobs:\n  a:\n    on: {push: }\n    script: x\n    pod: {containers: [{name: a, image: b}]}\n",
			wantErr: "/cobranch.yaml: line 3: job a needs exactly one of pod, script or webhook"},
		{name: "an image without a script", settings: "jobs:\n  a:\n    on: {push: }\n    pod: {containers: [{name: a, image: b}]}\n    image: x\n",
			wantErr: "/cobranch.yaml: line 5: image goes with script only"},
		{name: "a pod on the host", settings: "jobs:\n  a:\n    on: {push: }\n    pod: {hostNetwork: true, containers: [{name: a, image: b}]}\n",
			wantErr: "/cobranch.yaml: line 4: a job's pod may not set hostNetwork"},
		{name: "a webhook over http", settings: "jobs:\n  a:\n    on: {push: }\n    webhook: {url: \"http://x.example\", signingSecret: " + secretID + "}\n",
			wantErr: "/cobranch.yaml: line 4: url must be an https URL"},
		{name: "a webhook unsigned", settings: "jobs:\n  a:\n    on: {push: }\n    webhook: {url: \"https://x.example\"}\n",
			wantErr: "/cobranch.yaml: line 4: webhook needs a signingSecret, a secret's ID"},
		{name: "a secret by name", settings: "jobs:\n  a:\n    on: {push: }\n    script: x\n    secrets: {TOKEN: npm-token}\n",
			wantErr: "/cobranch.yaml: line 5: a secret is named by its ID"},
		{name: "a secret in Cobranch's variables", settings: "jobs:\n  a:\n    on: {push: }\n    script: x\n    secrets: {COBRANCH_TOKEN: " + secretID + "}\n",
			wantErr: `/cobranch.yaml: line 5: "COBRANCH_TOKEN" is no environment variable a secret may take`},
		{name: "an unknown permission", settings: "jobs:\n  a:\n    on: {push: }\n    script: x\n    permissions: [repo.write]\n",
			wantErr: `/cobranch.yaml: line 5: "repo.write" is no repository permission`},
		{name: "a day's timeout", settings: "jobs:\n  a:\n    on: {push: }\n    script: x\n    timeout: 25h\n",
			wantErr: "/cobranch.yaml: line 5: timeout must be a duration such as 30m or 2h, at most 24h"},
		{name: "following itself", settings: "jobs:\n  a:\n    on: {annotation: {names: [a]}}\n    script: x\n",
			wantErr: "/cobranch.yaml: line 3: job a can't follow its own annotation"},
		{name: "a bad status", settings: "jobs:\n  a:\n    on: {annotation: {names: [b], statuses: [passed]}}\n    script: x\n",
			wantErr: "/cobranch.yaml: line 3: statuses must list pending, in_progress, failed or successful"},
		{name: "a bad review state", settings: "jobs:\n  a:\n    on: {review: {states: [lgtm]}}\n    script: x\n",
			wantErr: "/cobranch.yaml: line 3: states must list approved, changes_requested or neutral"},
		{name: "a bad cron", settings: "jobs:\n  a:\n    on:\n      schedule:\n        - cron: \"0 25 * * *\"\n    script: x\n",
			wantErr: `/cobranch.yaml: line 5: the hour field takes 0 to 23, not "25"`},
		{name: "a branch pattern twice", settings: "jobs:\n  a:\n    on: {push: {branches: [main, main]}}\n    script: x\n",
			wantErr: `/cobranch.yaml: line 3: branches names "main" twice`},
	})
}

func TestSettingsRoundTrip(t *testing.T) {
	settings, err := code.ParseSettings([]byte("repository:\n  outsidePushes: run\njobs:\n  a:\n    on: {push: , schedule: [{cron: '*/5 * * * *'}]}\n" +
		"    script: x\n    timeout: 90m\n"))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var decoded code.Settings
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, settings) {
		t.Errorf("decoded %s as %+v, want %+v", encoded, decoded, settings)
	}
}
