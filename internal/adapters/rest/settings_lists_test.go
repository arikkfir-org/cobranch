package rest

import (
	"net/http"
	"testing"
)

// settingsLists are the lists of Acme's Settings pages that a read may wait on.
var settingsLists = []string{"/v1/orgs/acme/members", "/v1/orgs/acme/invitations", "/v1/orgs/acme/join-requests",
	"/v1/orgs/acme/roles", "/v1/orgs/acme/api-keys", "/v1/orgs/acme/secrets", "/v1/orgs/acme/agents"}

// pageLists are the lists and figures of Acme's other pages, and of its sidebar, that a read may wait on.
var pageLists = []string{"/v1/orgs/acme/repos", "/v1/orgs/acme/tasks", "/v1/orgs/acme/artifacts", "/v1/orgs/acme/overview",
	"/v1/orgs/acme/attention", "/v1/orgs/acme/activity", "/v1/orgs/acme/usage"}

func TestFollowedListsTagged(t *testing.T) {
	for _, list := range append(settingsLists, pageLists...) {
		t.Run(list, func(t *testing.T) {
			stale := http.Header{"If-None-Match": {`"stale"`}}
			runOrgCases(t, []orgCase{
				{name: "tagged", viewer: olivia, method: "GET", path: fixed(list), wantStatus: 200, wantTag: `"`},
				{name: "still the list the caller holds", viewer: olivia, method: "GET", path: fixed(list), wantStatus: 304, wantTag: `"`,
					headerFor: held(olivia, fixed(list))},
				{name: "a wait that sees no change", viewer: olivia, method: "GET", path: fixed(list + "?wait=1"), wantStatus: 304, wantTag: `"`,
					headerFor: held(olivia, fixed(list))},
				{name: "another list", viewer: olivia, method: "GET", path: fixed(list + "?wait=15"), header: stale, wantStatus: 200, wantTag: `"`},
				{name: "a wait without a tag answers at once", viewer: olivia, method: "GET", path: fixed(list + "?wait=15"), wantStatus: 200},
				{name: "a wait past 15 seconds", viewer: olivia, method: "GET", path: fixed(list + "?wait=16"), header: stale, wantStatus: 400, wantCode: "wait.invalid"},
				{name: "a wait as an outsider", viewer: nia, method: "GET", path: fixed(list + "?wait=1"), header: stale, wantStatus: 404, wantCode: "organization.not_found"},
			})
		})
	}
}
