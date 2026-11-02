// Package restclient calls Cobranch's REST API as an agent's harness does, as its task's own token, through the client
// oapi-codegen generates from api/openapi.yaml (restclient.gen.go). It implements the harness's Cobranch port, and the
// AI reviewer's API.
package restclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/harness"
)

// transcriptPage is how many entries a read of the transcript asks for at once: the most a page holds.
const transcriptPage = agents.MaxEntriesPage

// API calls the REST API as a task's token, which it reads from its file before each call, so that a token the
// controller renews reaches the next call without a restart.
type API struct {
	api       *ClientWithResponses
	tokenFile string
}

var _ harness.Cobranch = (*API)(nil)

// New returns an API of the REST API that the REST server at url serves, under /v1/, as the token in tokenFile,
// sending its requests through client, which retries what a retry may get past.
func New(url, tokenFile string, client *http.Client) (*API, error) {
	return newClient(url, tokenFile, retrying(client))
}

// newClient returns an API of the REST API at url, as the token in tokenFile, sending its requests through doer.
func newClient(url, tokenFile string, doer HttpRequestDoer) (*API, error) {
	c := &API{tokenFile: tokenFile}
	api, err := NewClientWithResponses(strings.TrimSuffix(url, "/")+"/v1", WithHTTPClient(doer), WithRequestEditorFn(c.authorize))
	if err != nil {
		return nil, fmt.Errorf("making a client of %s: %w", url, err)
	}
	c.api = api
	return c, nil
}

// authorize sends the token in its file as the request's bearer token.
func (c *API) authorize(_ context.Context, req *http.Request) error {
	token, err := os.ReadFile(c.tokenFile)
	if err != nil {
		return fmt.Errorf("reading the task's token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	return nil
}

// Work answers the task the token works (harness.Cobranch).
func (c *API) Work(ctx context.Context) (agents.Work, error) {
	resp, err := c.api.GetMyTaskWithResponse(ctx)
	if err != nil {
		return agents.Work{}, fmt.Errorf("asking which task the token works: %w", err)
	}
	if resp.JSON200 == nil {
		return agents.Work{}, refused("asking which task the token works", resp.StatusCode(), resp.Body)
	}
	my := resp.JSON200
	return agents.Work{Task: task(my.Task), OrgSlug: my.Organization, AgentInstructions: my.AgentInstructions, AgentEmail: my.AgentEmail,
		IssueNumber: deref(my.IssueNumber, 0), IssueKey: deref(my.IssueKey, "")}, nil
}

// Transcript lists every entry of the organization's task, in order, a page at a time (harness.Cobranch).
func (c *API) Transcript(ctx context.Context, org string, number int64) ([]agents.Entry, error) {
	var out []agents.Entry
	for {
		after, limit := len(out), transcriptPage
		if after > 0 {
			after = out[after-1].Seq
		}
		resp, err := c.api.ListTranscriptWithResponse(ctx, org, number, &ListTranscriptParams{After: &after, Limit: &limit})
		if err != nil {
			return nil, fmt.Errorf("reading the transcript: %w", err)
		}
		if resp.JSON200 == nil {
			return nil, refused("reading the transcript", resp.StatusCode(), resp.Body)
		}
		for _, e := range resp.JSON200.Entries {
			out = append(out, entry(e))
		}
		if len(resp.JSON200.Entries) < limit {
			return out, nil
		}
	}
}

// Append records e, the next entry of the organization's task, and answers it (harness.Cobranch).
func (c *API) Append(ctx context.Context, org string, number int64, e agents.Entry) (agents.Entry, error) {
	resp, err := c.api.AppendTranscriptWithResponse(ctx, org, number, TranscriptEntryInput{Seq: e.Seq, Kind: TranscriptEntryKind(e.Kind), Body: e.Body})
	if err != nil {
		return agents.Entry{}, fmt.Errorf("appending entry %d: %w", e.Seq, err)
	}
	switch {
	case resp.JSON201 != nil:
		return entry(*resp.JSON201), nil
	case resp.JSON200 != nil:
		return entry(*resp.JSON200), nil
	default:
		return agents.Entry{}, refused(fmt.Sprintf("appending entry %d", e.Seq), resp.StatusCode(), resp.Body)
	}
}

// refused is the failure of a call the API answered with status and a problem: harness.ErrRefused when it refused the
// token.
func refused(what string, status int, body []byte) error {
	var p Problem
	detail := strings.TrimSpace(string(body[:min(len(body), 300)]))
	if json.Unmarshal(body, &p) == nil && p.Code != "" {
		detail = p.Title
		if p.Detail != nil {
			detail = *p.Detail
		}
	}
	if status == http.StatusUnauthorized {
		return fmt.Errorf("%s: %w: %s", what, harness.ErrRefused, detail)
	}
	return fmt.Errorf("%s: the API answered %d, %s: %s", what, status, p.Code, detail)
}

// task is t as the agents service names it: what the harness reads of it.
func task(t Task) agents.Task {
	out := agents.Task{ID: t.Id, Number: t.Number, Kind: t.Kind, Title: t.Title, Instructions: t.Instructions, State: agents.TaskState(t.State),
		Agent: agents.Principal{ID: t.Agent.Id, DisplayName: t.Agent.DisplayName}, Summary: t.Summary, Reason: t.Reason, Version: t.Version}
	if t.AssignedBy != nil {
		out.AssignedBy = &agents.Principal{ID: t.AssignedBy.Id, DisplayName: t.AssignedBy.DisplayName}
	}
	out.RepoID, out.RepoSlug = deref(t.RepositoryId, uuid.Nil), deref(t.RepositorySlug, "")
	out.BranchID, out.Branch = deref(t.BranchId, uuid.Nil), deref(t.Branch, "")
	out.ThreadID, out.SHA = deref(t.ThreadId, uuid.Nil), deref(t.Sha, "")
	out.CreatedAt, out.StartedAt = t.CreatedAt, deref(t.StartedAt, time.Time{})
	return out
}

// entry is e as the agents service names it.
func entry(e TranscriptEntry) agents.Entry {
	return agents.Entry{Seq: e.Seq, Kind: agents.EntryKind(e.Kind), Body: e.Body, CreatedAt: e.CreatedAt}
}

// deref is *p, or fallback for nil.
func deref[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}
