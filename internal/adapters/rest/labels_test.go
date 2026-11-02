package rest

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/issues"
)

// withLabels is withIssues' fixture with Acme's labels Bug, red, and UI, blue, and ACME-1 carrying Bug.
func withLabels(viewer func(f *orgFixture) *auth.Authenticated) func(f *orgFixture) *auth.Authenticated {
	return withIssues(func(f *orgFixture) *auth.Authenticated {
		ctx := context.Background()
		bug, err := f.issues.CreateLabel(ctx, f.olivia.User, f.acme.ID, issues.LabelInput{Name: "Bug", Color: issues.ColorRed})
		if err != nil {
			panic(err)
		}
		if _, err := f.issues.CreateLabel(ctx, f.olivia.User, f.acme.ID, issues.LabelInput{Name: "UI", Color: issues.ColorBlue}); err != nil {
			panic(err)
		}
		if _, err := f.issues.Update(ctx, f.olivia.User, f.acme.ID, 1, issues.Patch{LabelIDs: &[]uuid.UUID{bug.ID}}); err != nil {
			panic(err)
		}
		return viewer(f)
	})
}

// labelID is the ID of Acme's label of that name.
func labelID(f *orgFixture, name string) string {
	labels, err := f.issues.Labels(context.Background(), f.olivia.User, f.acme.ID)
	if err != nil {
		panic(err)
	}
	for _, l := range labels {
		if l.Name == name {
			return l.ID.String()
		}
	}
	panic("no label " + name)
}

func labelPath(name string) func(f *orgFixture) string {
	return func(f *orgFixture) string { return "/v1/orgs/acme/labels/" + labelID(f, name) }
}

func TestLabelsAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a member lists them", viewer: withLabels(maxim), method: http.MethodGet, path: fixed("/v1/orgs/acme/labels"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				list := decode[struct{ Labels []Label }](t, body).Labels
				if len(list) != 2 || list[0].Name != "Bug" || list[0].Color != "red" || list[0].Issues != 1 || list[1].Name != "UI" {
					t.Errorf("labels = %s", body)
				}
			}},
		{name: "an Owner creates one", viewer: olivia, method: http.MethodPost, path: fixed("/v1/orgs/acme/labels"),
			body: `{"name":"Docs","color":"green","description":"Guides"}`, wantStatus: 201, wantHeader: http.Header{"Etag": {`"1"`}},
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if l := decode[Label](t, body); l.Name != "Docs" || l.Color != "green" || l.Description != "Guides" || l.Version != 1 {
					t.Errorf("label = %s", body)
				}
			}},
		{name: "a member creates none", viewer: maxim, method: http.MethodPost, path: fixed("/v1/orgs/acme/labels"), body: `{"name":"Docs"}`,
			wantStatus: 403},
		{name: "a name another has", viewer: withLabels(olivia), method: http.MethodPost, path: fixed("/v1/orgs/acme/labels"), body: `{"name":"bug"}`,
			wantStatus: 409, wantCode: "label.taken"},
		{name: "a colour that isn't", viewer: olivia, method: http.MethodPost, path: fixed("/v1/orgs/acme/labels"), body: `{"name":"x","color":"pink"}`,
			wantStatus: 400},
		{name: "one, by ID", viewer: withLabels(maxim), method: http.MethodGet, path: labelPath("UI"), wantStatus: 200,
			wantHeader: http.Header{"Etag": {`"1"`}, "Cache-Control": {"private, no-cache"}}},
		{name: "one the caller holds", viewer: withLabels(maxim), method: http.MethodGet, path: labelPath("UI"),
			header: http.Header{"If-None-Match": {`"1"`}}, wantStatus: 304},
		{name: "one that isn't there", viewer: maxim, method: http.MethodGet, path: fixed("/v1/orgs/acme/labels/" + uuid.NewString()),
			wantStatus: 404, wantCode: "label.not_found"},
		{name: "an outsider learns nothing", viewer: nia, method: http.MethodGet, path: fixed("/v1/orgs/acme/labels"), wantStatus: 404,
			wantCode: "organization.not_found"},
	})
}

func TestChangeLabelsAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "an Owner renames one", viewer: withLabels(olivia), method: http.MethodPatch, path: labelPath("Bug"), body: `{"name":"Defect","color":"yellow"}`,
			header: mergePatchAt(`"1"`), wantStatus: 200, wantHeader: http.Header{"Etag": {`"2"`}}, check: func(t *testing.T, f *orgFixture, body []byte) {
				if l := decode[Label](t, body); l.Name != "Defect" || l.Color != "yellow" {
					t.Errorf("label = %s", body)
				}
				if i, err := f.issues.Get(context.Background(), f.max.User, f.acme.ID, 1); err != nil || len(i.Labels) != 1 || i.Labels[0].Name != "Defect" {
					t.Errorf("ACME-1's labels = %+v, %v; want Defect", i.Labels, err)
				}
			}},
		{name: "without If-Match", viewer: withLabels(olivia), method: http.MethodPatch, path: labelPath("Bug"), body: `{"name":"Defect"}`,
			header: mergePatch, wantStatus: 428},
		{name: "a stale one", viewer: withLabels(olivia), method: http.MethodPatch, path: labelPath("Bug"), body: `{"name":"Defect"}`,
			header: mergePatchAt(`"4"`), wantStatus: 412},
		{name: "to another's name", viewer: withLabels(olivia), method: http.MethodPatch, path: labelPath("Bug"), body: `{"name":"ui"}`,
			header: mergePatchAt(`"1"`), wantStatus: 409, wantCode: "label.taken"},
		{name: "a member changes none", viewer: withLabels(maxim), method: http.MethodPatch, path: labelPath("Bug"), body: `{"name":"Defect"}`,
			header: mergePatchAt(`"1"`), wantStatus: 403},
		{name: "an Owner deletes one", viewer: withLabels(olivia), method: http.MethodDelete, path: labelPath("Bug"), header: http.Header{"If-Match": {`"1"`}},
			wantStatus: 204, check: func(t *testing.T, f *orgFixture, _ []byte) {
				if i, err := f.issues.Get(context.Background(), f.max.User, f.acme.ID, 1); err != nil || len(i.Labels) != 0 {
					t.Errorf("ACME-1's labels = %+v, %v; want none", i.Labels, err)
				}
			}},
		{name: "a deletion without If-Match", viewer: withLabels(olivia), method: http.MethodDelete, path: labelPath("Bug"), wantStatus: 428},
	})
}

func TestIssueLabelsAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "an issue shows its labels", viewer: withLabels(maxim), method: http.MethodGet, path: fixed("/v1/orgs/acme/issues/1"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if i := decode[Issue](t, body); len(i.Labels) != 1 || i.Labels[0].Name != "Bug" || i.Labels[0].Color != "red" {
					t.Errorf("issue = %s", body)
				}
			}},
		{name: "an issue without labels shows none", viewer: withLabels(maxim), method: http.MethodGet, path: fixed("/v1/orgs/acme/issues/3"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if i := decode[Issue](t, body); i.Labels == nil || len(i.Labels) != 0 {
					t.Errorf("issue = %s", body)
				}
			}},
		{name: "a member labels one", viewer: withLabels(maxim), method: http.MethodPatch, path: fixed("/v1/orgs/acme/issues/3"),
			header: mergePatchAt(`"1-ACME"`), wantStatus: 200, bodyOf: func(f *orgFixture) string {
				return `{"labelIds":["` + labelID(f, "UI") + `","` + labelID(f, "Bug") + `"]}`
			}, check: func(t *testing.T, _ *orgFixture, body []byte) {
				if i := decode[Issue](t, body); len(i.Labels) != 2 || i.Labels[0].Name != "Bug" || i.Labels[1].Name != "UI" {
					t.Errorf("issue = %s", body)
				}
			}},
		{name: "a member creates one labelled", viewer: withLabels(maxim), method: http.MethodPost, path: fixed("/v1/orgs/acme/issues"), wantStatus: 201,
			bodyOf: func(f *orgFixture) string { return `{"title":"Fix it","labelIds":["` + labelID(f, "Bug") + `"]}` },
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if i := decode[Issue](t, body); len(i.Labels) != 1 || i.Labels[0].Name != "Bug" {
					t.Errorf("issue = %s", body)
				}
			}},
		{name: "another organization's label", viewer: withLabels(maxim), method: http.MethodPost, path: fixed("/v1/orgs/acme/issues"),
			body: `{"title":"Fix it","labelIds":["` + uuid.NewString() + `"]}`, wantStatus: 400, wantCode: "labels.invalid"},
		{name: "those carrying a label", viewer: withLabels(maxim), method: http.MethodGet,
			path: func(f *orgFixture) string { return "/v1/orgs/acme/issues?label=" + labelID(f, "Bug") }, wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if list := decode[struct{ Issues []Issue }](t, body).Issues; len(list) != 1 || list[0].Identifier != "ACME-1" {
					t.Errorf("issues = %s", body)
				}
			}},
		{name: "a label that isn't an ID", viewer: maxim, method: http.MethodGet, path: fixed("/v1/orgs/acme/issues?label=bug"), wantStatus: 400},
	})
}
