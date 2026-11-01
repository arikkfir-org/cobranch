package rest

import (
	"slices"
	"strings"
	"testing"
)

// commitNames lists the first letters of commits' names.
func commitNames(commits []Commit) []string {
	var out []string
	for _, c := range commits {
		out = append(out, c.Sha[:1])
	}
	return out
}

// commitPage is a page of a branch's history.
type commitPage struct{ Commits []Commit }

// history checks that a page lists the commits want names, by their first letters.
func history(want ...string) func(t *testing.T, f *orgFixture, body []byte) {
	return func(t *testing.T, _ *orgFixture, body []byte) {
		if got := commitNames(decode[commitPage](t, body).Commits); !slices.Equal(got, want) {
			t.Errorf("commits = %v, want %v: %s", got, want, body)
		}
	}
}

func TestBranchesAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "the branches, main first", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/branches"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				got := decode[struct{ Branches []Branch }](t, body).Branches
				if len(got) != 3 || got[0].Name != "main" || got[0].Commit == nil || got[0].Commit.Sha != shaOf("c") || got[1].Name != "feature" ||
					got[2].Name != "feature/login" {
					t.Errorf("branches = %s", body)
				}
			}},
		{name: "main, with what a merge into it needs", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/branches/main"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				const want = `"checks":[],"commit":`
				if !strings.Contains(string(body), want) || !strings.Contains(string(body), `"requirements":{"annotations":["ci/build"],"linearHistory":false}`) ||
					strings.Contains(string(body), `"status"`) || strings.Contains(string(body), `"base"`) {
					t.Errorf("main = %s, want its requirements and no status", body)
				}
			}},
		{name: "a branch with its status, checks, facts and last merge", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/branches/feature"),
			wantStatus: 200, check: func(t *testing.T, f *orgFixture, body []byte) {
				for _, want := range []string{`"base":"main"`, `"status":"blocked"`, `"ahead":1`, `"behind":2`,
					`"checks":[{"kind":"annotation","met":false,"name":"ci/build"},{"count":2,"kind":"up_to_date","met":false}]`,
					`"merge":{"at":"2026-11-01T09:00:00Z","by":{"displayName":"Olivia","id":"` + f.olivia.User.ID.String() + `"},"head":"` + shaOf("a") + `"}`} {
					if !strings.Contains(string(body), want) {
						t.Errorf("feature = %s, want %s", body, want)
					}
				}
				if strings.Contains(string(body), `"requirements"`) {
					t.Errorf("feature = %s, want no requirements, as /cobranch.yaml names none", body)
				}
			}},
		{name: "a branch named with a slash, escaped, that no push has worked out", viewer: olivia, method: "GET",
			path: fixed("/v1/orgs/acme/repos/website/branches/feature%2Flogin"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if b := decode[Branch](t, body); b.Name != "feature/login" || b.Status == nil || *b.Status != Processing || b.Ahead != nil {
					t.Errorf("feature/login = %s, want it processing, with no facts", body)
				}
			}},
		{name: "a branch that doesn't live", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/branches/gone"),
			wantStatus: 404, wantCode: "branch.not_found"},
		{name: "a Member who doesn't read it sees no branch", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/repos/website/branches/main"),
			wantStatus: 404, wantCode: "repository.not_found"},
		{name: "a Member who doesn't read it sees no branches", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/repos/website/branches"),
			wantStatus: 404, wantCode: "repository.not_found"},
		{name: "a token with repo.read alone lists them", viewer: oliviaToken("repo.read"), method: "GET", path: fixed("/v1/orgs/acme/repos/website/branches"),
			wantStatus: 200},
		{name: "an organization that isn't there", viewer: olivia, method: "GET", path: fixed("/v1/orgs/nobody/repos/website/branches"),
			wantStatus: 404, wantCode: "repository.not_found"},
	})
}

func TestCommitsAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "main's history, nearest first", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/commits"), wantStatus: 200,
			check: history("c", "b", "a")},
		{name: "another branch's", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/commits?branch=feature"), wantStatus: 200,
			check: history("d", "a")},
		{name: "a page links the next", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/commits?limit=2"), wantStatus: 200,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				history("c", "b")(t, f, body)
				rec := f.call(&f.olivia, "GET", "/v1/orgs/acme/repos/website/commits?limit=2", "")
				want := `<?branch=main&cursor=` + shaOf("a") + `&limit=2>; rel="next"`
				if got := rec.Header().Get("Link"); got != want {
					t.Errorf("Link = %q, want %q", got, want)
				}
			}},
		{name: "the next page, the last", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/commits?limit=2&cursor=" + shaOf("a")), wantStatus: 200,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				history("a")(t, f, body)
				if rec := f.call(&f.olivia, "GET", "/v1/orgs/acme/repos/website/commits?limit=2&cursor="+shaOf("a"), ""); rec.Header().Get("Link") != "" {
					t.Errorf("the last page links %q", rec.Header().Get("Link"))
				}
			}},
		{name: "a branch that doesn't live", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/commits?branch=gone"),
			wantStatus: 404, wantCode: "branch.not_found"},
		{name: "a cursor that is no object name", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/commits?cursor=HEAD"),
			wantStatus: 400, wantCode: "cursor.invalid"},
		{name: "a page too long", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/commits?limit=101"),
			wantStatus: 400, wantCode: "limit.invalid"},
		{name: "an Owner's token without repo.read sees no history", viewer: oliviaToken("org.read"), method: "GET", path: fixed("/v1/orgs/acme/repos/website/commits"),
			wantStatus: 404, wantCode: "repository.not_found"},
		{name: "a token with repo.read alone reads it", viewer: oliviaToken("repo.read"), method: "GET", path: fixed("/v1/orgs/acme/repos/website/commits"),
			wantStatus: 200, check: history("c", "b", "a")},
	})
}

func TestCommitAPI(t *testing.T) {
	runOrgCases(t, []orgCase{
		{name: "a commit with its push", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/commits/" + shaOf("c")), wantStatus: 200,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				c := decode[Commit](t, body)
				if c.Sha != shaOf("c") || !slices.Equal(c.Parents, []string{shaOf("b")}) || c.Push == nil || c.Push.Id != f.push.ID ||
					c.Push.Pusher == nil || c.Push.Pusher.DisplayName != "Olivia" || c.Push.Transport != "https" ||
					c.Author.Principal == nil || c.Author.Principal.Id != f.olivia.User.ID {
					t.Errorf("commit = %s", body)
				}
			}},
		{name: "a list's commits come without pushes", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/commits?limit=1"), wantStatus: 200,
			check: func(t *testing.T, _ *orgFixture, body []byte) {
				if got := decode[commitPage](t, body).Commits; len(got) != 1 || got[0].Push != nil {
					t.Errorf("commits = %s", body)
				}
			}},
		{name: "a commit the repository doesn't hold", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/commits/" + shaOf("e")),
			wantStatus: 404, wantCode: "commit.not_found"},
		{name: "an abbreviated name", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/commits/cccccc"),
			wantStatus: 400, wantCode: "sha.invalid"},
		{name: "a Member who doesn't read it sees no commit", viewer: maxim, method: "GET", path: fixed("/v1/orgs/acme/repos/website/commits/" + shaOf("c")),
			wantStatus: 404, wantCode: "repository.not_found"},
		{name: "a token with repo.read alone reads it", viewer: oliviaToken("repo.read"), method: "GET", path: fixed("/v1/orgs/acme/repos/website/commits/" + shaOf("c")),
			wantStatus: 200},
	})
}
