package graphql

import (
	"context"
	"strings"
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

// withLabelIDs is onAcme's document with $Bug and $UI the IDs of Acme's labels.
func withLabelIDs(document string) func(*orgFixture) string {
	return func(f *orgFixture) string {
		labels, err := f.issues.Labels(context.Background(), f.olivia.User, f.acme.ID)
		if err != nil {
			panic(err)
		}
		for _, l := range labels {
			document = strings.ReplaceAll(document, "$"+l.Name, l.ID.String())
		}
		return onAcme(document)(f)
	}
}

func TestLabels(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a member lists them", viewer: withLabels(maxim),
			document: fixed(`{ organization(slug: "acme") { labels { name color issues version } } }`),
			want:     []string{`"labels":[{"name":"Bug","color":"RED","issues":1,"version":1},{"name":"UI","color":"BLUE","issues":0,"version":1}]`}},
		{name: "an issue shows its labels", viewer: withLabels(maxim),
			document: fixed(`{ organization(slug: "acme") { issue(number: 1) { labels { name color } } } }`),
			want:     []string{`"labels":[{"name":"Bug","color":"RED"}]`}},
		{name: "those carrying a label", viewer: withLabels(maxim),
			document: withLabelIDs(`{ organization(slug: "acme") { issues(filter: {labelIds: ["$Bug"]}) { issues { identifier } } } }`),
			want:     []string{`"issues":[{"identifier":"ACME-1"}]`}},
		{name: "a member labels one", viewer: withLabels(maxim),
			document: withLabelIDs(`mutation { updateIssue(organizationId: "$org", number: 3, input: {labelIds: ["$UI", "$Bug"]}) { labels { name } } }`),
			want:     []string{`"labels":[{"name":"Bug"},{"name":"UI"}]`}},
		{name: "a member creates one labelled", viewer: withLabels(maxim),
			document: withLabelIDs(`mutation { createIssue(organizationId: "$org", input: {title: "Fix it", labelIds: ["$UI"]}) { labels { name } } }`),
			want:     []string{`"labels":[{"name":"UI"}]`}},
		{name: "a label that isn't an ID", viewer: withLabels(maxim),
			document: onAcme(`mutation { createIssue(organizationId: "$org", input: {title: "Fix it", labelIds: ["bug"]}) { identifier } }`),
			wantCode: badUserInput, wantReason: "labelIds.invalid"},
		{name: "an Owner creates one", viewer: olivia,
			document: onAcme(`mutation { createLabel(organizationId: "$org", input: {name: "Docs", color: GREEN}) { name color version } }`),
			want:     []string{`"name":"Docs","color":"GREEN","version":1`}},
		{name: "a name another has", viewer: withLabels(olivia),
			document: onAcme(`mutation { createLabel(organizationId: "$org", input: {name: "bug"}) { name } }`),
			wantCode: conflict, wantReason: "label.taken"},
		{name: "a member creates none", viewer: maxim,
			document: onAcme(`mutation { createLabel(organizationId: "$org", input: {name: "Docs"}) { name } }`), wantCode: forbidden},
		{name: "an Owner renames one", viewer: withLabels(olivia),
			document: withLabelIDs(`mutation { updateLabel(organizationId: "$org", id: "$Bug", input: {name: "Defect", color: YELLOW}, expectedVersion: 1) { name color version } }`),
			want:     []string{`"name":"Defect","color":"YELLOW","version":2`}},
		{name: "a rename from an old version", viewer: withLabels(olivia),
			document: withLabelIDs(`mutation { updateLabel(organizationId: "$org", id: "$Bug", input: {name: "Defect"}, expectedVersion: 2) { name } }`),
			wantCode: conflict, wantReason: "precondition.failed"},
		{name: "an Owner deletes one", viewer: withLabels(olivia),
			document: withLabelIDs(`mutation { deleteLabel(organizationId: "$org", id: "$Bug", expectedVersion: 1) }`),
			want:     []string{`"deleteLabel":true`}},
		{name: "one that isn't there", viewer: olivia,
			document: onAcme(`mutation { deleteLabel(organizationId: "$org", id: "` + uuid.NewString() + `") }`),
			wantCode: notFound, wantReason: "label.not_found"},
	})
}
