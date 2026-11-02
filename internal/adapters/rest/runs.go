package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"cobranch.dev/internal/services/code"
)

// run is r as the API shows it. Its definition and trigger travel as the domain encodes them, the shape the contract
// gives them.
func run(r code.Run) (Run, error) {
	out := Run{Id: r.ID, Number: r.Number, Job: Principal{Id: r.Job.ID, DisplayName: r.Job.DisplayName}, Sha: r.SHA, Ref: r.Ref,
		State: RunState(r.State), Reason: r.Reason, StartedBy: principal(r.StartedBy), QueuedAt: r.QueuedAt.UTC(),
		StartedAt: utc(r.StartedAt), EndedAt: utc(r.EndedAt), Version: r.Version}
	if r.CauseRun != 0 {
		out.CauseRun = &r.CauseRun
	}
	if err := recode(r.Definition, &out.Definition); err != nil {
		return Run{}, fmt.Errorf("encoding run #%d's definition: %w", r.Number, err)
	}
	out.Definition.Kind = JobKind(r.Definition.Kind())
	if err := recode(r.Trigger, &out.Trigger); err != nil {
		return Run{}, fmt.Errorf("encoding run #%d's trigger: %w", r.Number, err)
	}
	return out, nil
}

// recode copies in to out through JSON.
func recode(in, out any) error {
	encoded, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("encoding: %w", err)
	}
	if err := json.Unmarshal(encoded, out); err != nil {
		return fmt.Errorf("decoding: %w", err)
	}
	return nil
}

// runTag is r's ETag: its version, quoted.
func runTag(r code.Run) string {
	return `"` + strconv.FormatInt(r.Version, 10) + `"`
}

// ListRuns lists a page of the repository's runs, and links the next.
func (a api) ListRuns(ctx context.Context, req ListRunsRequestObject) (ListRunsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	sha, before, limit := "", int64(0), 0
	if p := req.Params; p.SHA != nil {
		sha = *p.SHA
	}
	if p := req.Params; p.Cursor != nil {
		if before, err = strconv.ParseInt(*p.Cursor, 10, 64); err != nil || before < 1 {
			return nil, refuse(http.StatusBadRequest, "cursor.invalid", "Start from the cursor the previous page's Link names.")
		}
	}
	if p := req.Params; p.Limit != nil {
		limit = *p.Limit
	}
	page, err := a.code.Runs(ctx, v, repo.ID, sha, before, limit)
	if err != nil {
		return nil, fail(err, "repository")
	}
	out := ListRuns200JSONResponse{}
	out.Body.Runs = make([]Run, 0, len(page.Runs))
	for _, r := range page.Runs {
		shown, err := run(r)
		if err != nil {
			return nil, err
		}
		out.Body.Runs = append(out.Body.Runs, shown)
	}
	if page.Next != 0 {
		next := url.Values{"cursor": {strconv.FormatInt(page.Next, 10)}}
		if sha != "" {
			next.Set("sha", sha)
		}
		if limit != 0 {
			next.Set("limit", strconv.Itoa(limit))
		}
		link := `<?` + next.Encode() + `>; rel="next"`
		out.Headers.Link = &link
	}
	return out, nil
}

// GetRun answers one of the repository's runs, or 304 while the caller holds its version.
func (a api) GetRun(ctx context.Context, req GetRunRequestObject) (GetRunResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	r, err := a.code.Run(ctx, v, repo.ID, req.Number)
	if err != nil {
		return nil, fail(err, "run")
	}
	tag := runTag(r)
	if matches(req.Params.IfNoneMatch, tag) {
		return GetRun304Response{Headers: GetRun304ResponseHeaders{ETag: tag}}, nil
	}
	shown, err := run(r)
	if err != nil {
		return nil, err
	}
	return GetRun200JSONResponse{Body: shown, Headers: GetRun200ResponseHeaders{ETag: tag}}, nil
}

// CancelRun cancels one of the repository's runs for the viewer.
func (a api) CancelRun(ctx context.Context, req CancelRunRequestObject) (CancelRunResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	r, err := a.code.CancelRun(ctx, v, repo.ID, req.Number)
	if err != nil {
		return nil, fail(err, "run")
	}
	shown, err := run(r)
	if err != nil {
		return nil, err
	}
	return CancelRun200JSONResponse{Body: shown, Headers: CancelRun200ResponseHeaders{ETag: runTag(r)}}, nil
}

// StartRun queues one of the repository's waiting runs for the viewer, and names it in Location.
func (a api) StartRun(ctx context.Context, req StartRunRequestObject) (StartRunResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	r, err := a.code.StartRun(ctx, v, repo.ID, req.Number)
	if err != nil {
		return nil, fail(err, "run")
	}
	shown, err := run(r)
	if err != nil {
		return nil, err
	}
	location := "/v1/orgs/" + url.PathEscape(req.Org) + "/repos/" + url.PathEscape(req.Repo) + "/runs/" + strconv.FormatInt(r.Number, 10)
	return StartRun202JSONResponse{Body: shown, Headers: StartRun202ResponseHeaders{ETag: runTag(r), Location: location}}, nil
}

// utc is t in UTC, or nil without one.
func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	at := t.UTC()
	return &at
}
