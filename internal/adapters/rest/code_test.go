package rest

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
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

func TestEveryParentAPI(t *testing.T) {
	const commits = "/v1/orgs/acme/repos/website/commits"
	runOrgCases(t, []orgCase{
		{name: "every parent, newest first", viewer: olivia, method: "GET", path: fixed(commits + "?parents=all"), wantStatus: 200, check: history("c", "b", "a")},
		{name: "a page links the next, which holds still", viewer: olivia, method: "GET", path: fixed(commits + "?parents=all&limit=2"), wantStatus: 200,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				history("c", "b")(t, f, body)
				link := f.call(&f.olivia, "GET", commits+"?parents=all&limit=2", "").Header().Get("Link")
				next, ok := strings.CutPrefix(link, "<?")
				next, _, _ = strings.Cut(next, ">")
				if !ok || !strings.Contains(next, "parents=all") || !strings.Contains(next, "limit=2") {
					t.Fatalf("Link = %q, want the next page of every parent", link)
				}
				f.objects.Refs["main"] = shaOf("b")
				rec := f.call(&f.olivia, "GET", commits+"?"+next, "")
				history("a")(t, f, rec.Body.Bytes())
				if rec.Header().Get("Link") != "" {
					t.Errorf("the last page links %q", rec.Header().Get("Link"))
				}
			}},
		{name: "from a tag", viewer: olivia, method: "GET", path: fixed(commits + "?parents=all&ref=v1.0.0"), wantStatus: 200, check: history("b", "a")},
		{name: "first parents from a tag", viewer: olivia, method: "GET", path: fixed(commits + "?ref=v1.0.0"), wantStatus: 200, check: history("b", "a")},
		{name: "a ref that names nothing", viewer: olivia, method: "GET", path: fixed(commits + "?parents=all&ref=gone"), wantStatus: 404, wantCode: "ref.not_found"},
		{name: "a cursor of nothing", viewer: olivia, method: "GET", path: fixed(commits + "?parents=all&cursor=nonsense"), wantStatus: 400, wantCode: "cursor.invalid"},
		{name: "parents that are neither", viewer: olivia, method: "GET", path: fixed(commits + "?parents=some"), wantStatus: 400},
		{name: "a page's ETag answers 304 while it holds", viewer: olivia, method: "GET", path: fixed(commits + "?parents=all"), wantStatus: 200,
			check: func(t *testing.T, f *orgFixture, _ []byte) {
				tag := f.call(&f.olivia, "GET", commits+"?parents=all", "").Header().Get("ETag")
				revalidate := func() *httptest.ResponseRecorder {
					req := httptest.NewRequest("GET", commits+"?parents=all", nil)
					req.Header.Set("If-None-Match", tag)
					return f.serve(&f.olivia, req)
				}
				if rec := revalidate(); tag == "" || rec.Code != 304 || rec.Body.Len() != 0 {
					t.Fatalf("ETag %q answered %d", tag, rec.Code)
				}
				f.codeStore.Add(f.website.ID, f.push, code.Commit{SHA: shaOf("c"), Parents: []string{shaOf("b")}, Message: "Reworded\n"})
				if rec := revalidate(); rec.Code != 200 || rec.Header().Get("ETag") == tag {
					t.Errorf("a page whose commit changed answered %d with ETag %q", rec.Code, rec.Header().Get("ETag"))
				}
			}},
		{name: "without a Git server", viewer: olivia, method: "GET", path: fixed(commits + "?parents=all"), wantStatus: 200,
			check: func(t *testing.T, f *orgFixture, _ []byte) {
				f.objects.Fail = code.ErrUnavailable
				if rec := f.call(&f.olivia, "GET", commits+"?parents=all", ""); rec.Code != 503 {
					t.Errorf("every parent without a Git server answered %d, want 503", rec.Code)
				}
			}},
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

// asked checks that the Git server was asked for one merge of feature at d, by method, for Olivia, past main's
// requirements when bypass, and that the answer names main at d.
func asked(method code.MergeMethod, bypass bool) func(t *testing.T, f *orgFixture, body []byte) {
	return func(t *testing.T, f *orgFixture, body []byte) {
		want := code.MergeRequest{Branch: "feature", Head: shaOf("d"), Base: "main", Method: method,
			By: code.Merger{ID: f.olivia.User.ID, Name: "Olivia", Email: "olivia@example.com"}, Bypass: bypass}
		if len(f.objects.Merges) != 1 || f.objects.Merges[0] != want {
			t.Errorf("the Git server was asked for %+v, want %+v", f.objects.Merges, want)
		}
		if got := decode[MergeResult](t, body); got != (MergeResult{Base: "main", Head: shaOf("d"), Commit: shaOf("d")}) {
			t.Errorf("merge = %s, want main at feature's head", body)
		}
	}
}

func TestMergeBranchAPI(t *testing.T) {
	const feature = "/v1/orgs/acme/repos/website/branches/feature/merge"
	// failingFor is Olivia once the Git server fails every merge with err.
	failingFor := func(err error) func(f *orgFixture) *auth.Authenticated {
		return func(f *orgFixture) *auth.Authenticated {
			f.objects.Fail = err
			return &f.olivia
		}
	}
	runOrgCases(t, []orgCase{
		{name: "an Owner merges a Blocked branch past main's requirements", viewer: olivia, method: "POST", path: fixed(feature), wantStatus: 200,
			check: asked(code.MethodMerge, true)},
		{name: "at the head the viewer saw, by a merge commit", viewer: olivia, method: "POST", path: fixed(feature),
			body: `{"head":"` + shaOf("d") + `","method":"merge"}`, wantStatus: 200, check: asked(code.MethodMerge, true)},
		{name: "a head the branch moved from", viewer: olivia, method: "POST", path: fixed(feature), body: `{"head":"` + shaOf("c") + `"}`,
			wantStatus: 409, wantCode: "branch.moved"},
		{name: "a fast-forward of a branch behind main", viewer: olivia, method: "POST", path: fixed(feature), body: `{"method":"fast_forward"}`,
			wantStatus: 409, wantCode: code.MergeNotFastForward},
		{name: "a method there is none of", viewer: olivia, method: "POST", path: fixed(feature), body: `{"method":"rebase"}`,
			wantStatus: 400, wantCode: "method.invalid"},
		{name: "a token without repo.bypass may not merge a Blocked branch", viewer: oliviaToken("repo.read", "repo.merge"), method: "POST",
			path: fixed(feature), wantStatus: 409, wantCode: code.MergeNotReady},
		{name: "a token without repo.merge may not merge", viewer: oliviaToken("repo.read"), method: "POST", path: fixed(feature),
			wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "main merges into nothing", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/repos/website/branches/main/merge"),
			wantStatus: 400, wantCode: "branch.default"},
		{name: "a branch that doesn't live", viewer: olivia, method: "POST", path: fixed("/v1/orgs/acme/repos/website/branches/gone/merge"),
			wantStatus: 404, wantCode: "branch.not_found"},
		{name: "a Member who doesn't read it finds no repository", viewer: maxim, method: "POST", path: fixed(feature),
			wantStatus: 404, wantCode: "repository.not_found"},
		{name: "a merge the Git server won't make", viewer: failingFor(&code.MergeError{Code: code.MergeConflict, Message: "feature and main both change README.md"}),
			method: "POST", path: fixed(feature), wantStatus: 409, wantCode: code.MergeConflict},
		{name: "a branch that moved while the Git server merged it", viewer: failingFor(code.ErrBranchMoved), method: "POST", path: fixed(feature),
			wantStatus: 409, wantCode: "branch.moved"},
		{name: "an installation without a Git server", viewer: failingFor(code.ErrUnavailable), method: "POST", path: fixed(feature),
			wantStatus: 503, wantCode: "git.unavailable"},
	})
}

// deleted checks that the Git server was asked for one deletion of branch at head, for Olivia, past its
// requirements when bypass.
func deleted(branch, head string, bypass bool) func(t *testing.T, f *orgFixture, body []byte) {
	return func(t *testing.T, f *orgFixture, _ []byte) {
		want := code.DeleteRequest{Branch: branch, Head: head, By: f.olivia.User.ID, Bypass: bypass}
		if len(f.objects.Deletions) != 1 || f.objects.Deletions[0] != want {
			t.Errorf("the Git server was asked for %+v, want %+v", f.objects.Deletions, want)
		}
	}
}

func TestDeleteBranchAPI(t *testing.T) {
	const feature = "/v1/orgs/acme/repos/website/branches/feature"
	at := func(sha string) http.Header { return http.Header{"If-Match": {`"` + sha + `"`}} }
	// failingFor is Olivia once the Git server fails every deletion with err.
	failingFor := func(err error) func(f *orgFixture) *auth.Authenticated {
		return func(f *orgFixture) *auth.Authenticated {
			f.objects.Fail = err
			return &f.olivia
		}
	}
	runOrgCases(t, []orgCase{
		{name: "at the head the viewer saw", viewer: olivia, method: "DELETE", path: fixed(feature), header: at(shaOf("d")), wantStatus: 204,
			check: deleted("feature", shaOf("d"), false)},
		{name: "wherever it is", viewer: olivia, method: "DELETE", path: fixed(feature), header: http.Header{"If-Match": {"*"}}, wantStatus: 204,
			check: deleted("feature", shaOf("d"), false)},
		{name: "a branch named with a slash, escaped", viewer: olivia, method: "DELETE", path: fixed(feature + "%2Flogin"), header: at(shaOf("c")),
			wantStatus: 204, check: deleted("feature/login", shaOf("c"), false)},
		{name: "a head the branch moved from", viewer: olivia, method: "DELETE", path: fixed(feature), header: at(shaOf("c")),
			wantStatus: 412, wantCode: "precondition.failed"},
		{name: "a weak tag", viewer: olivia, method: "DELETE", path: fixed(feature), header: http.Header{"If-Match": {`W/"` + shaOf("d") + `"`}},
			wantStatus: 412, wantCode: "precondition.failed"},
		{name: "no If-Match", viewer: olivia, method: "DELETE", path: fixed(feature), wantStatus: 428, wantCode: "precondition.required"},
		{name: "main is never deleted", viewer: olivia, method: "DELETE", path: fixed("/v1/orgs/acme/repos/website/branches/main"), header: at(shaOf("c")),
			wantStatus: 409, wantCode: code.DeleteDefault},
		{name: "a token without repo.push may not delete", viewer: oliviaToken("repo.read", "repo.merge"), method: "DELETE", path: fixed(feature),
			header: at(shaOf("d")), wantStatus: 403, wantCode: "auth.forbidden"},
		{name: "a branch that doesn't live", viewer: olivia, method: "DELETE", path: fixed("/v1/orgs/acme/repos/website/branches/gone"),
			header: http.Header{"If-Match": {"*"}}, wantStatus: 404, wantCode: "branch.not_found"},
		{name: "a Member who doesn't read it finds no repository", viewer: maxim, method: "DELETE", path: fixed(feature), header: at(shaOf("d")),
			wantStatus: 404, wantCode: "repository.not_found"},
		{name: "a deletion the push refuses", viewer: failingFor(&code.DeleteError{Code: code.DeleteRefused, Message: "refused"}), method: "DELETE",
			path: fixed(feature), header: at(shaOf("d")), wantStatus: 409, wantCode: code.DeleteRefused},
		{name: "a branch that moved while the Git server deleted it", viewer: failingFor(code.ErrBranchMoved), method: "DELETE", path: fixed(feature),
			header: at(shaOf("d")), wantStatus: 412, wantCode: "precondition.failed"},
		{name: "an installation without a Git server", viewer: failingFor(code.ErrUnavailable), method: "DELETE", path: fixed(feature),
			header: at(shaOf("d")), wantStatus: 503, wantCode: "git.unavailable"},
	})
}

func TestDeletedBranchAPI(t *testing.T) {
	const old = "/v1/orgs/acme/repos/website/deleted-branches/old"
	// withOld is viewer once old was deleted, at d, by Olivia, after she merged it.
	withOld := func(viewer func(f *orgFixture) *auth.Authenticated) func(f *orgFixture) *auth.Authenticated {
		return func(f *orgFixture) *auth.Authenticated {
			by := &code.Principal{ID: f.olivia.User.ID, DisplayName: "Olivia"}
			f.codeStore.Deleted[f.website.ID] = []code.DeletedBranch{{Name: "old", Head: shaOf("d"), CreatedAt: now, DeletedAt: now.Add(time.Hour),
				DeletedBy: by, Merge: &code.Merge{Head: shaOf("d"), By: by, At: now}}}
			return viewer(f)
		}
	}
	runOrgCases(t, []orgCase{
		{name: "who deleted it and when, and its merge", viewer: withOld(olivia), method: "GET", path: fixed(old), wantStatus: 200,
			check: func(t *testing.T, f *orgFixture, body []byte) {
				got := decode[DeletedBranch](t, body)
				if got.Name != "old" || got.Head != shaOf("d") || !got.DeletedAt.Equal(now.Add(time.Hour)) || got.DeletedBy == nil ||
					got.DeletedBy.Id != f.olivia.User.ID || got.Merge == nil || got.Merge.Head != shaOf("d") {
					t.Errorf("old = %s", body)
				}
			}},
		{name: "a token with repo.read alone reads it", viewer: withOld(oliviaToken("repo.read")), method: "GET", path: fixed(old), wantStatus: 200},
		{name: "a live branch's name", viewer: olivia, method: "GET", path: fixed("/v1/orgs/acme/repos/website/deleted-branches/feature"),
			wantStatus: 404, wantCode: "branch.not_found"},
		{name: "a Member who doesn't read it finds no repository", viewer: withOld(maxim), method: "GET", path: fixed(old),
			wantStatus: 404, wantCode: "repository.not_found"},
	})
}
