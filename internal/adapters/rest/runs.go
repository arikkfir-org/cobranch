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
	"cobranch.dev/internal/services/jobs"
)

// run is r as the API shows it. Its definition and trigger travel as the domain encodes them, the shape the contract
// gives them.
func run(r code.Run) (Run, error) {
	out := Run{Id: r.ID, Number: r.Number, Job: Principal{Id: r.Job.ID, DisplayName: r.Job.DisplayName}, Sha: r.SHA, Ref: r.Ref,
		State: RunState(r.State), Reason: r.Reason, StartedBy: principal(r.StartedBy), CausedBy: actor(r.CausedBy), QueuedAt: r.QueuedAt.UTC(),
		StartedAt: utc(r.StartedAt), EndedAt: utc(r.EndedAt), Version: r.Version}
	if r.CauseRun != 0 {
		out.CauseRun = &r.CauseRun
	}
	out.Attempt = max(r.Attempt, 1)
	if r.RerunOf != 0 {
		out.RerunOf = &r.RerunOf
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
	q, err := runsQuery(req.Params)
	if err != nil {
		return nil, err
	}
	page, err := a.code.Runs(ctx, v, repo.ID, q)
	if err != nil {
		return nil, fail(err, "repository")
	}
	out := ListRuns200JSONResponse{}
	out.Body.Runs = make([]Run, 0, len(page.Runs))
	out.Body.Total = page.Total
	for _, r := range page.Runs {
		shown, err := run(r)
		if err != nil {
			return nil, err
		}
		out.Body.Runs = append(out.Body.Runs, shown)
	}
	if page.Next != 0 {
		link := `<?` + runsNext(req.Params, page.Next).Encode() + `>; rel="next"`
		out.Headers.Link = &link
	}
	return out, nil
}

// runsQuery is the page of runs p asks for: its filters, where it starts and how long it is.
func runsQuery(p ListRunsParams) (code.RunsQuery, error) {
	q := code.RunsQuery{SHA: orEmpty(p.SHA), Job: orEmpty(p.Job), Branch: orEmpty(p.Branch), Query: orEmpty(p.Q)}
	if p.State != nil {
		for _, st := range *p.State {
			q.States = append(q.States, jobs.State(st))
		}
	}
	if p.Cursor != nil {
		before, err := strconv.ParseInt(*p.Cursor, 10, 64)
		if err != nil || before < 1 {
			return code.RunsQuery{}, refuse(http.StatusBadRequest, "cursor.invalid", "Start from the cursor the previous page's Link names.")
		}
		q.Before = before
	}
	if p.Limit != nil {
		q.Limit = *p.Limit
	}
	return q, nil
}

// runsNext is the query of the page after the one p asks for, which starts before run number next: the same filters and
// length.
func runsNext(p ListRunsParams, next int64) url.Values {
	out := url.Values{"cursor": {strconv.FormatInt(next, 10)}}
	for name, v := range map[string]*string{"sha": p.SHA, "job": p.Job, "branch": p.Branch, "q": p.Q} {
		if v != nil && *v != "" {
			out.Set(name, *v)
		}
	}
	if p.State != nil {
		for _, st := range *p.State {
			out.Add("state", string(st))
		}
	}
	if p.Limit != nil {
		out.Set("limit", strconv.Itoa(*p.Limit))
	}
	return out
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
	return StartRun202JSONResponse{Body: shown, Headers: StartRun202ResponseHeaders{ETag: runTag(r), Location: runURL(req.Org, req.Repo, r.Number)}}, nil
}

// RerunRun runs an ended run again as its next attempt.
func (a api) RerunRun(ctx context.Context, req RerunRunRequestObject) (RerunRunResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	r, err := a.code.RerunRun(ctx, v, repo.ID, req.Number)
	if err != nil {
		return nil, fail(err, "run")
	}
	shown, err := run(r)
	if err != nil {
		return nil, err
	}
	return RerunRun201JSONResponse{Body: shown, Headers: RerunRun201ResponseHeaders{ETag: runTag(r), Location: runURL(req.Org, req.Repo, r.Number)}}, nil
}

// RunJob starts a manual job at its branch's head.
func (a api) RunJob(ctx context.Context, req RunJobRequestObject) (RunJobResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	r, err := a.code.RunJob(ctx, v, repo.ID, req.BranchName, req.Job)
	if err != nil {
		return nil, fail(err, "job")
	}
	shown, err := run(r)
	if err != nil {
		return nil, err
	}
	return RunJob201JSONResponse{Body: shown, Headers: RunJob201ResponseHeaders{ETag: runTag(r), Location: runURL(req.Org, req.Repo, r.Number)}}, nil
}

// PostEvent posts an event, which queues the runs of the jobs that follow it.
func (a api) PostEvent(ctx context.Context, req PostEventRequestObject) (PostEventResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	repo, err := a.codeRepo(ctx, v, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	in := code.EventInput{Name: req.Body.Name}
	if req.Body.Branch != nil {
		in.Branch = *req.Body.Branch
	}
	if req.Body.Data != nil {
		if in.Data, err = json.Marshal(req.Body.Data); err != nil {
			return nil, fmt.Errorf("encoding the event's data: %w", err)
		}
	}
	runs, err := a.code.PostEvent(ctx, v, repo.ID, in)
	if err != nil {
		return nil, fail(err, "repository")
	}
	out := PostedEvent{Runs: make([]Run, 0, len(runs))}
	sha := ""
	for _, r := range runs {
		shown, err := run(r)
		if err != nil {
			return nil, err
		}
		out.Runs, sha = append(out.Runs, shown), r.SHA
	}
	location := "/v1/orgs/" + url.PathEscape(req.Org) + "/repos/" + url.PathEscape(req.Repo) + "/runs"
	if sha != "" {
		location += "?sha=" + sha
	}
	return PostEvent202JSONResponse{Body: out, Headers: PostEvent202ResponseHeaders{Location: location}}, nil
}

// runURL is the URL of a repository's run.
func runURL(org, repo string, number int64) string {
	return "/v1/orgs/" + url.PathEscape(org) + "/repos/" + url.PathEscape(repo) + "/runs/" + strconv.FormatInt(number, 10)
}

// utc is t in UTC, or nil without one.
func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	at := t.UTC()
	return &at
}
