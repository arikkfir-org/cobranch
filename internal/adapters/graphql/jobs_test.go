package graphql

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/jobs"
)

// withRuns adds three runs to website before next's setup: #1 of ci/build at b, succeeded; #2 of ci/build at c,
// waiting for a member; #3 of deploy, a pod, at c, queued.
func withRuns(next func(*orgFixture) *auth.Authenticated) func(*orgFixture) *auth.Authenticated {
	return func(f *orgFixture) *auth.Authenticated {
		build := jobs.Job{Name: "ci/build", Script: "make test", Permissions: jobs.DefaultPermissions,
			On: jobs.Triggers{Push: &jobs.PushTrigger{Branches: []string{"main"}}}, Timeout: jobs.Duration(30 * time.Minute)}
		deploy := jobs.Job{Name: "deploy", Pod: json.RawMessage(`{"containers":[{"name":"main","image":"alpine"}]}`),
			Permissions: jobs.DefaultPermissions, Secrets: map[string]uuid.UUID{"TOKEN": uuid.MustParse("6f1d5c1e-8a8b-4f6e-9a57-1d6e0b9c2f10")}}
		actor := f.max.User.ID
		for _, r := range []code.Run{
			{SHA: shaOf("b"), Definition: build, State: jobs.StateSucceeded},
			{SHA: shaOf("c"), Definition: build, State: jobs.StateWaiting, Reason: "waits for a member to start it"},
			{SHA: shaOf("c"), Definition: deploy, State: jobs.StateQueued},
		} {
			r.Job, r.Ref = code.Principal{ID: uuid.New(), DisplayName: r.Definition.Name}, "refs/heads/main"
			r.QueuedAt = time.Date(2026, 11, 1, 9, 0, 0, 0, time.FixedZone("IST", 2*3600))
			r.Trigger = jobs.Event{Kind: jobs.EventPush, SHA: r.SHA, Ref: r.Ref, ActorID: actor}
			f.codeStore.AddRun(f.website.ID, r)
		}
		return next(f)
	}
}

func TestRunQueries(t *testing.T) {
	repo := func(fields string) func(*orgFixture) string {
		return fixed(`{ organization(slug: "acme") { repository(slug: "website") { ` + fields + ` } } }`)
	}
	runOrgCases(t, []orgCase{
		{name: "a Reader lists them, newest first", viewer: withRuns(maxHolding(auth.RoleReader)),
			document: repo(`runs { runs { number state job { displayName } } nextCursor }`),
			want: []string{`"runs":[{"number":3,"state":"QUEUED","job":{"displayName":"deploy"}},{"number":2,"state":"WAITING","job":{"displayName":"ci/build"}},` +
				`{"number":1,"state":"SUCCEEDED","job":{"displayName":"ci/build"}}],"nextCursor":null`}},
		{name: "a page and the next", viewer: withRuns(maxHolding(auth.RoleReader)),
			document: repo(`first: runs(first: 2) { runs { number } nextCursor } next: runs(first: 2, after: "2") { runs { number } nextCursor }`),
			want:     []string{`"first":{"runs":[{"number":3},{"number":2}],"nextCursor":"2"}`, `"next":{"runs":[{"number":1}],"nextCursor":null}`}},
		{name: "at a commit", viewer: withRuns(maxHolding(auth.RoleReader)), document: repo(`runs(sha: "` + shaOf("b") + `") { runs { number } }`),
			want: []string{`"runs":[{"number":1}]`}},
		{name: "a script's definition and trigger", viewer: withRuns(maxHolding(auth.RoleReader)),
			document: repo(`run(number: 2) { reason queuedAt trigger { kind ref name state } definition { name kind on script pod timeout permissions checkout } }`),
			want: []string{`"reason":"waits for a member to start it","queuedAt":"2026-11-01T07:00:00Z"`,
				`"trigger":{"kind":"push","ref":"refs/heads/main","name":"","state":""}`,
				`"definition":{"name":"ci/build","kind":"script","on":{"push":{"branches":["main"]}},"script":"make test","pod":null,"timeout":"30m0s","permissions":["annotations.write","repo.read"],"checkout":false}`}},
		{name: "a pod's", viewer: withRuns(maxHolding(auth.RoleReader)), document: repo(`run(number: 3) { definition { kind pod secrets script } }`),
			want: []string{`"definition":{"kind":"pod","pod":{"containers":[{"image":"alpine","name":"main"}]},"secrets":{"TOKEN":"6f1d5c1e-8a8b-4f6e-9a57-1d6e0b9c2f10"},"script":null}`}},
		{name: "one that isn't there", viewer: withRuns(maxHolding(auth.RoleReader)), document: repo(`run(number: 9) { number }`), want: []string{`"run":null`}},
		{name: "a cursor of nothing", viewer: withRuns(maxHolding(auth.RoleReader)), document: repo(`runs(after: "next") { nextCursor }`),
			wantCode: badUserInput, wantReason: "cursor.invalid"},
		{name: "too many a page", viewer: withRuns(maxHolding(auth.RoleReader)), document: repo(`runs(first: 101) { nextCursor }`),
			wantCode: badUserInput, wantReason: "limit.invalid"},
		{name: "too few a page", viewer: withRuns(maxHolding(auth.RoleReader)), document: repo(`runs(first: 0) { nextCursor }`),
			wantCode: badUserInput, wantReason: "limit.invalid"},
	})
}

func TestRunMutations(t *testing.T) {
	change := func(mutation string, number string) func(*orgFixture) string {
		return func(f *orgFixture) string {
			return `mutation { ` + mutation + `(repositoryId: "` + f.website.ID.String() + `", number: ` + number + `) { number state reason startedBy { displayName } version } }`
		}
	}
	runOrgCases(t, []orgCase{
		{name: "a Developer cancels one", viewer: withRuns(maxHolding(auth.RoleDeveloper)), document: change("cancelRun", "3"),
			want: []string{`"cancelRun":{"number":3,"state":"CANCELLED","reason":"cancelled by Max","startedBy":null,"version":2}`}},
		{name: "one that has ended", viewer: withRuns(maxHolding(auth.RoleDeveloper)), document: change("cancelRun", "1"), wantCode: conflict, wantReason: "run.ended"},
		{name: "a Reader may not cancel", viewer: withRuns(maxHolding(auth.RoleReader)), document: change("cancelRun", "3"), wantCode: forbidden},
		{name: "a Developer starts a waiting one", viewer: withRuns(maxHolding(auth.RoleDeveloper)), document: change("startRun", "2"),
			want: []string{`"startRun":{"number":2,"state":"QUEUED","reason":"","startedBy":{"displayName":"Max"},"version":2}`}},
		{name: "one that doesn't wait", viewer: withRuns(maxHolding(auth.RoleDeveloper)), document: change("startRun", "3"), wantCode: conflict, wantReason: "run.not_waiting"},
		{name: "a Reader may not start", viewer: withRuns(maxHolding(auth.RoleReader)), document: change("startRun", "2"), wantCode: forbidden},
		{name: "one that isn't there", viewer: withRuns(maxHolding(auth.RoleDeveloper)), document: change("startRun", "9"), wantCode: notFound, wantReason: "run.not_found"},
		{name: "a Member who doesn't read it finds nothing", viewer: withRuns(maxim), document: change("cancelRun", "3"), wantCode: notFound, wantReason: "run.not_found"},
	})
}

// withJobs gives website a main at c whose /cobranch.yaml declares release, which members start by hand or by event
// release/requested, and ci/build, on pushes, before next's setup, with withRuns' runs.
func withJobs(next func(*orgFixture) *auth.Authenticated) func(*orgFixture) *auth.Authenticated {
	return withRuns(func(f *orgFixture) *auth.Authenticated {
		f.codeStore.BranchList[f.website.ID] = append(f.codeStore.BranchList[f.website.ID], code.Branch{Name: "main", Head: shaOf("c")})
		f.codeStore.Defined[f.website.ID] = map[string][]jobs.Job{"main": {
			{Name: "release", Script: "make release", On: jobs.Triggers{Manual: &jobs.ManualTrigger{}, Event: &jobs.EventTrigger{Names: []string{"release/requested"}}}},
			{Name: "ci/build", Script: "make test", On: jobs.Triggers{Push: &jobs.PushTrigger{}}},
		}}
		return next(f)
	})
}

func TestRunTriggerMutations(t *testing.T) {
	on := func(document string) func(*orgFixture) string {
		return func(f *orgFixture) string {
			return strings.ReplaceAll(document, "REPO", f.website.ID.String())
		}
	}
	runOrgCases(t, []orgCase{
		{name: "a Developer re-runs an ended run", viewer: withRuns(maxHolding(auth.RoleDeveloper)),
			document: on(`mutation { rerunRun(repositoryId: "REPO", number: 1) { number attempt rerunOf state startedBy { displayName } } }`),
			want:     []string{`"rerunRun":{"number":4,"attempt":2,"rerunOf":1,"state":"QUEUED","startedBy":{"displayName":"Max"}}`}},
		{name: "re-running one that hasn't ended", viewer: withRuns(maxHolding(auth.RoleDeveloper)),
			document: on(`mutation { rerunRun(repositoryId: "REPO", number: 3) { number } }`), wantCode: conflict, wantReason: "run.active"},
		{name: "a Reader may not re-run", viewer: withRuns(maxHolding(auth.RoleReader)),
			document: on(`mutation { rerunRun(repositoryId: "REPO", number: 1) { number } }`), wantCode: forbidden},
		{name: "a Developer starts a manual job", viewer: withJobs(maxHolding(auth.RoleDeveloper)),
			document: on(`mutation { runJob(repositoryId: "REPO", branch: "main", job: "release") { number attempt trigger { kind name } } }`),
			want:     []string{`"runJob":{"number":4,"attempt":1,"trigger":{"kind":"manual","name":"release"}}`}},
		{name: "a job without a manual trigger", viewer: withJobs(maxHolding(auth.RoleDeveloper)),
			document: on(`mutation { runJob(repositoryId: "REPO", branch: "main", job: "ci/build") { number } }`), wantCode: conflict, wantReason: "job.not_manual"},
		{name: "a Developer posts an event", viewer: withJobs(maxHolding(auth.RoleDeveloper)),
			document: on(`mutation { postEvent(repositoryId: "REPO", name: "release/requested", data: {env: "production"}) { number trigger { kind name data } } }`),
			want:     []string{`"postEvent":[{"number":4,"trigger":{"kind":"event","name":"release/requested","data":{"env":"production"}}}]`}},
		{name: "an event on a branch that isn't there", viewer: withJobs(maxHolding(auth.RoleDeveloper)),
			document: on(`mutation { postEvent(repositoryId: "REPO", name: "release/requested", branch: "gone") { number } }`),
			wantCode: badUserInput, wantReason: "branch.unknown"},
		{name: "a Reader may not post", viewer: withJobs(maxHolding(auth.RoleReader)),
			document: on(`mutation { postEvent(repositoryId: "REPO", name: "release/requested") { number } }`), wantCode: forbidden},
	})
}

// logArchive is code.ArchivedLogs over a map of runs' logs, by run ID.
type logArchive map[uuid.UUID]map[string][]byte

func (a logArchive) Read(_ context.Context, _, runID uuid.UUID) (map[string][]byte, error) {
	return a[runID], nil
}

// withLogs keeps logs of #1, which withRuns adds, as it ended: its checkout's, and its script's, which isn't UTF-8.
func withLogs(next func(*orgFixture) *auth.Authenticated) func(*orgFixture) *auth.Authenticated {
	return withRuns(func(f *orgFixture) *auth.Authenticated {
		first := &f.codeStore.RunList[f.website.ID][0]
		started := first.QueuedAt
		first.StartedAt = &started
		f.code.WithRunLogs(logArchive{first.ID: {"main": []byte("built \xff"), "cobranch-checkout": []byte("checked out")}}, nil, 0)
		return next(f)
	})
}

func TestRunLogs(t *testing.T) {
	repo := func(fields string) func(*orgFixture) string {
		return fixed(`{ organization(slug: "acme") { repository(slug: "website") { ` + fields + ` } } }`)
	}
	runOrgCases(t, []orgCase{
		{name: "a Reader reads an ended run's, the checkout's first", viewer: withLogs(maxHolding(auth.RoleReader)),
			document: repo(`run(number: 1) { logs { container text live } }`),
			want:     []string{`"logs":[{"container":"cobranch-checkout","text":"checked out","live":false},{"container":"main","text":"built �","live":false}]`}},
		{name: "none of a run that hasn't started", viewer: withLogs(maxHolding(auth.RoleReader)),
			document: repo(`runs { runs { number logs { container } } }`),
			want:     []string{`{"number":3,"logs":[]},{"number":2,"logs":[]},{"number":1,"logs":[{"container":"cobranch-checkout"},{"container":"main"}]}`}},
	})
}
