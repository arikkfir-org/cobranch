package restclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/harness"
)

// planID is the one artifact the API holds, at version 2 and ETag "4"; versions made from another ETag are refused.
var planID = uuid.MustParse("0190f1b2-0000-7000-8000-0000000000a1")

// artifact answers the artifact calls, recording each in chats as its method, its path and its body.
func (a *api) artifact(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body) // a test's body always reads
	a.chats = append(a.chats, r.Method+" "+r.URL.EscapedPath()+" "+r.Header.Get("If-Match")+" "+strings.TrimSpace(string(body)))
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	plan := Artifact{Id: planID, Title: "Plan", Kind: "document", Head: ptr[int32](2), MediaType: ptr[ArtifactMediaType]("text/markdown"), CreatedAt: &at, UpdatedAt: &at}
	switch path := strings.TrimPrefix(r.URL.Path, "/v1/orgs/acme/"); {
	case r.Method == http.MethodPost && (path == "artifacts" || path == "repos/website/artifacts"):
		var in NewArtifact
		_ = json.Unmarshal(body, &in) // the client sends what it built
		made := Artifact{Id: *in.Id, Title: in.Title, Kind: in.Kind, Head: ptr[int32](1), MediaType: &in.MediaType, CreatedAt: &at, UpdatedAt: &at}
		if path != "artifacts" {
			made.Repository = &ArtifactRepository{Id: uuid.New(), Slug: "website"}
		}
		w.Header().Set("ETag", `"1"`)
		answer(w, http.StatusCreated, made)
	case path == "artifacts/"+planID.String():
		w.Header().Set("ETag", `"4"`)
		answer(w, http.StatusOK, plan)
	case path == "artifacts/"+planID.String()+"/versions/0":
		answer(w, http.StatusOK, ArtifactVersion{Id: uuid.New(), ArtifactId: planID, Number: 2, MediaType: "text/markdown", Body: ptr("# Plan\n"),
			Note: "Added the rollout", CreatedAt: at})
	case path == "artifacts/"+planID.String()+"/versions" && r.Header.Get("If-Match") != `"4"`:
		problem(w, http.StatusPreconditionFailed, "precondition.failed", "The artifact changed.")
	case path == "artifacts/"+planID.String()+"/versions":
		w.Header().Set("ETag", `"5"`)
		answer(w, http.StatusCreated, ArtifactVersion{Id: uuid.New(), ArtifactId: planID, Number: 3, MediaType: "text/markdown", CreatedAt: at})
	default:
		problem(w, http.StatusNotFound, "artifact.not_found", "No such artifact.")
	}
}

func TestArtifacts(t *testing.T) {
	t.Parallel()
	a := &api{}
	c, _ := client(t, a)
	ctx, made := context.Background(), uuid.MustParse("0190f1b2-0000-7000-8000-0000000000a2")
	got, err := c.CreateArtifact(ctx, "acme", harness.NewArtifact{ID: made, Repo: "website", Branch: "tidy", Title: "Notes", Kind: "report",
		Format: "text/markdown", Body: "# Notes", Note: "First"})
	if err != nil || got.ID != made || got.Repo != "website" || got.Head != 1 || got.ETag != `"1"` {
		t.Errorf("CreateArtifact() = %+v, %v", got, err)
	}
	if got, err := c.CreateArtifact(ctx, "acme", harness.NewArtifact{ID: made, Title: "Notes", Kind: "report", Format: "text/markdown", Body: "# Notes"}); err != nil || got.Repo != "" {
		t.Errorf("CreateArtifact() of none = %+v, %v", got, err)
	}
	read, err := c.Artifact(ctx, "acme", planID, 0)
	if err != nil || read.Title != "Plan" || read.Head != 2 || read.ETag != `"4"` || read.Version.Number != 2 || read.Version.Body != "# Plan\n" ||
		read.Version.Format != "text/markdown" || read.Version.Note != "Added the rollout" {
		t.Errorf("Artifact() = %+v, %v", read, err)
	}
	if _, err := c.Artifact(ctx, "acme", made, 0); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("Artifact() of none = %v", err)
	}
	version := harness.ArtifactVersion{ID: made, Format: "text/markdown", Body: "# Plan, again"}
	if n, tag, err := c.AddArtifactVersion(ctx, "acme", planID, `"4"`, version); err != nil || n != 3 || tag != `"5"` {
		t.Errorf("AddArtifactVersion() = %d, %q, %v", n, tag, err)
	}
	if _, _, err := c.AddArtifactVersion(ctx, "acme", planID, `"3"`, version); !errors.Is(err, harness.ErrArtifactChanged) {
		t.Errorf("AddArtifactVersion() from an old ETag = %v", err)
	}
	checkArtifactCalls(t, a.chats, made)
}

// checkArtifactCalls checks the calls TestArtifacts made, as the API took them.
func checkArtifactCalls(t *testing.T, calls []string, made uuid.UUID) {
	t.Helper()
	plan := "/v1/orgs/acme/artifacts/" + planID.String()
	want := []string{
		`POST /v1/orgs/acme/repos/website/artifacts  {"body":"# Notes","branch":"tidy","id":"` + made.String() + `","kind":"report","mediaType":"text/markdown","note":"First","title":"Notes"}`,
		`POST /v1/orgs/acme/artifacts  {"body":"# Notes","id":"` + made.String() + `","kind":"report","mediaType":"text/markdown","title":"Notes"}`,
		"GET " + plan + "  ",
		"GET " + plan + "/versions/0  ",
		"GET /v1/orgs/acme/artifacts/" + made.String() + "  ",
		`POST ` + plan + `/versions "4" {"body":"# Plan, again","id":"` + made.String() + `","mediaType":"text/markdown"}`,
		`POST ` + plan + `/versions "3" {"body":"# Plan, again","id":"` + made.String() + `","mediaType":"text/markdown"}`,
	}
	if len(calls) != len(want) {
		t.Fatalf("the API took %q", calls)
	}
	for i, w := range want {
		if strings.TrimSpace(calls[i]) != strings.TrimSpace(w) {
			t.Errorf("call %d = %q, want %q", i+1, calls[i], w)
		}
	}
}
