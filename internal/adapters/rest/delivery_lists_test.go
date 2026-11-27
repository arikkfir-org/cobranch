package rest

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// npmAccess is the URL of who holds Secret reader on the fixture's secret npm, which it adds the first time.
func npmAccess(f *orgFixture) string {
	npm := f.secretNamed("npm")
	if npm.ID == uuid.Nil {
		npm, _ = f.withSecrets()
	}
	return "/v1/orgs/acme/secrets/" + npm.ID.String() + "/access"
}

// deliveryLists are what website's Delivery page, an environment's page and card, its Artifacts tab and its Settings'
// General, Access and Secrets tabs read, and who reads a secret, which a read may wait on, by name.
var deliveryLists = map[string]func(*orgFixture) string{
	"environments": fixed(environmentsPath), "a branch's environments": fixed(environmentsPath + "?branch=main"), "an environment": fixed(productionPath),
	"its objects": fixed(productionPath + "/objects"), "its deliveries": fixed(productionPath + "/deliveries?kind=deliver&kind=teardown"),
	"targets": fixed("/v1/orgs/acme/repos/website/targets"), "the repository": fixed("/v1/orgs/acme/repos/website"),
	"its access": fixed("/v1/orgs/acme/repos/website/access"), "its secrets": fixed("/v1/orgs/acme/repos/website/secrets"),
	"its artifacts": fixed("/v1/orgs/acme/repos/website/artifacts"), "who reads a secret": npmAccess,
}

func TestDeliveryListsTagged(t *testing.T) {
	viewer := withEnvironments(olivia)
	for name, list := range deliveryLists {
		at := func(query string) func(*orgFixture) string {
			return func(f *orgFixture) string {
				p := list(f)
				if strings.Contains(p, "?") {
					return p + "&" + query
				}
				return p + "?" + query
			}
		}
		t.Run(name, func(t *testing.T) {
			stale := http.Header{"If-None-Match": {`"stale"`}}
			runOrgCases(t, []orgCase{
				{name: "tagged", viewer: viewer, method: "GET", path: list, wantStatus: 200, wantTag: `"`},
				{name: "still the list the caller holds", viewer: viewer, method: "GET", path: list, wantStatus: 304, wantTag: `"`,
					headerFor: held(olivia, list)},
				{name: "a wait that sees no change", viewer: viewer, method: "GET", path: at("wait=1"), wantStatus: 304, wantTag: `"`,
					headerFor: held(olivia, list)},
				{name: "another list", viewer: viewer, method: "GET", path: at("wait=15"), header: stale, wantStatus: 200, wantTag: `"`},
				{name: "a wait without a tag answers at once", viewer: viewer, method: "GET", path: at("wait=15"), wantStatus: 200},
				{name: "a wait past 15 seconds", viewer: viewer, method: "GET", path: at("wait=16"), header: stale, wantStatus: 400, wantCode: "wait.invalid"},
				{name: "a wait as an outsider", viewer: withEnvironments(nia), method: "GET", path: at("wait=1"), header: stale, wantStatus: 404},
			})
		})
	}
}

func TestDeliveryWaitGone(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "an environment torn down", viewer: olivia, method: "GET", path: fixed(productionPath + "?wait=1"),
			header: http.Header{"If-None-Match": {`"1-x"`}}, wantStatus: 404, wantCode: "environment.not_found"},
	})
}
