package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/live/livetest"
)

func TestModelListsTagged(t *testing.T) {
	for _, list := range []string{"/v1/orgs/acme/model-providers", "/v1/orgs/acme/models"} {
		t.Run(list, func(t *testing.T) {
			stale := http.Header{"If-None-Match": {`"stale"`}}
			runOrgCases(t, []orgCase{
				{name: "tagged", viewer: maxim, method: "GET", path: onProvider(t, fixed(list)), wantStatus: 200, wantTag: `"`},
				{name: "still the list the caller holds", viewer: maxim, method: "GET", path: fixed(list), wantStatus: 304, wantTag: `"`,
					headerFor: func(f *orgFixture) http.Header { f.withProvider(t); return held(maxim, fixed(list))(f) }},
				{name: "a wait that sees no change", viewer: maxim, method: "GET", path: fixed(list + "?wait=1"), wantStatus: 304, wantTag: `"`,
					headerFor: held(maxim, fixed(list))},
				{name: "another list", viewer: maxim, method: "GET", path: fixed(list + "?wait=15"), header: stale, wantStatus: 200, wantTag: `"`},
				{name: "a wait without a tag answers at once", viewer: maxim, method: "GET", path: fixed(list + "?wait=15"), wantStatus: 200},
				{name: "a wait past 15 seconds", viewer: maxim, method: "GET", path: fixed(list + "?wait=16"), header: stale, wantStatus: 400, wantCode: "wait.invalid"},
				{name: "a wait as an outsider", viewer: nia, method: "GET", path: fixed(list + "?wait=1"), header: stale, wantStatus: 404, wantCode: "organization.not_found"},
			})
		})
	}
}

func TestModelListsWaitForAChange(t *testing.T) {
	tests := []struct {
		name string
		list string
		// change changes Acme's providers or models through the service, as Olivia.
		change func(t *testing.T, f *orgFixture)
		// names names what the list answers.
		names func(t *testing.T, body []byte) []string
		want  []string
	}{
		{name: "a provider connected", list: "/v1/orgs/acme/model-providers", change: func(t *testing.T, f *orgFixture) {
			if _, _, err := f.agents.SetModelProvider(t.Context(), f.olivia.User, f.acme.ID, "openai", f.providerKey, agents.NoVersion); err != nil {
				t.Fatal(err)
			}
		}, names: func(t *testing.T, body []byte) []string {
			var out []string
			for _, p := range decode[ModelProviderList](t, body).ModelProviders {
				out = append(out, p.Name)
			}
			return out
		}, want: []string{"anthropic", "openai"}},
		{name: "a model added", list: "/v1/orgs/acme/models", change: func(t *testing.T, f *orgFixture) {
			if _, _, err := f.agents.PutModel(t.Context(), f.olivia.User, f.acme.ID, "anthropic", "b-model", agents.ModelInput{}, agents.NoVersion); err != nil {
				t.Fatal(err)
			}
		}, names: func(t *testing.T, body []byte) []string {
			var out []string
			for _, m := range decode[ModelList](t, body).Models {
				out = append(out, m.Name)
			}
			return out
		}, want: []string{"a-model", "b-model"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := waitForListChange(t, tt.list, tt.change)
			if names := tt.names(t, got); !slices.Equal(names, tt.want) {
				t.Errorf("after the change = %v; want %v", names, tt.want)
			}
		})
	}
}

// waitForListChange has Max follow Acme's list at path, with anthropic and a-model added, makes change while the read
// waits, and answers the body of the read's answer, which must be 200 with a new ETag.
func waitForListChange(t *testing.T, path string, change func(*testing.T, *orgFixture)) []byte {
	t.Helper()
	f := newOrgFixture(t)
	f.withProvider(t)
	notifier := &livetest.Notifier{}
	f.agents.WithNotifier(notifier)
	tag := held(maxim, fixed(path))(f)
	answered := make(chan *httptest.ResponseRecorder, 1)
	go func() { answered <- f.callWith(&f.max, "GET", path+"?wait=15", "", tag) }()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for notifier.Watching(f.acme.ID) == 0 && ctx.Err() == nil {
		time.Sleep(10 * time.Millisecond)
	}
	change(t, f)
	notifier.Wake(f.acme.ID)
	select {
	case rec := <-answered:
		if rec.Code != http.StatusOK || rec.Header().Get("ETag") == tag.Get("If-None-Match") {
			t.Fatalf("after the change = %d with ETag %q; want 200 and a new ETag: %s", rec.Code, rec.Header().Get("ETag"), rec.Body)
		}
		return rec.Body.Bytes()
	case <-time.After(10 * time.Second):
		t.Fatal("no answer after the change")
		return nil
	}
}
