package graphql

import (
	"encoding/json"
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
