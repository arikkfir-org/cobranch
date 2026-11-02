package rest

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/auth"
)

// defaultTranscriptPage is how many entries a page holds unless the request names a limit.
const defaultTranscriptPage = 100

// transcriptEntry is e as the API shows it, with its ETag: an entry never changes, so its first version is its only
// one.
func transcriptEntry(e agents.Entry) (TranscriptEntry, string, error) {
	body := TranscriptEntry{Seq: e.Seq, Kind: TranscriptEntryKind(e.Kind), Body: e.Body, CreatedAt: e.CreatedAt.UTC()}
	tag, err := versionTag(1, body)
	return body, tag, err
}

// transcriptPath is where entry seq of the organization's task number is, relative to the API's base.
func transcriptPath(org string, number int64, seq int) string {
	return taskPath(org, number) + "/transcript/" + strconv.Itoa(seq)
}

// transcriptOrg finds the organization of the transcript a request names. A task token finds its own task's by the
// task it works, without org.read, which an agent holding only repository roles lacks, and agents.Service then holds it
// to that task; any other request finds it as agentOrg does.
func (a api) transcriptOrg(ctx context.Context, slug string) (agents.Viewer, uuid.UUID, string, error) {
	if who, ok := auth.Viewer(ctx); ok && who.Credential.Kind == auth.CredentialTaskToken {
		t, own, err := a.agents.Work(ctx, who.User)
		if err != nil {
			return agents.Viewer{}, uuid.Nil, "", fail(err, "task")
		}
		if own == slug {
			return who.User, t.OrgID, own, nil
		}
	}
	return a.agentOrg(ctx, slug)
}

// GetMyTask answers the task the request's task token works, for the agent's harness to start from.
func (a api) GetMyTask(ctx context.Context, _ GetMyTaskRequestObject) (GetMyTaskResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	t, slug, err := a.agents.Work(ctx, v)
	if err != nil {
		return nil, fail(err, "task")
	}
	return GetMyTask200JSONResponse{Organization: slug, Task: task(t)}, nil
}

// ListTranscript lists a page of the entries of one of the organization's tasks, in order.
func (a api) ListTranscript(ctx context.Context, req ListTranscriptRequestObject) (ListTranscriptResponseObject, error) {
	v, orgID, _, err := a.transcriptOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	after, limit := 0, defaultTranscriptPage
	if req.Params.After != nil {
		after = *req.Params.After
	}
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	entries, err := a.agents.Transcript(ctx, v, orgID, req.Number, after, limit)
	if err != nil {
		return nil, fail(err, "task")
	}
	out := ListTranscript200JSONResponse{}
	out.Body.Entries = make([]TranscriptEntry, 0, len(entries))
	for _, e := range entries {
		shown, _, err := transcriptEntry(e)
		if err != nil {
			return nil, err
		}
		out.Body.Entries = append(out.Body.Entries, shown)
	}
	if len(entries) == limit {
		next := url.Values{"after": {strconv.Itoa(entries[len(entries)-1].Seq)}, "limit": {strconv.Itoa(limit)}}
		link := `<?` + next.Encode() + `>; rel="next"`
		out.Headers.Link = &link
	}
	return out, nil
}

// AppendTranscript appends the next entry to the transcript of one of the organization's tasks, as its own token.
func (a api) AppendTranscript(ctx context.Context, req AppendTranscriptRequestObject) (AppendTranscriptResponseObject, error) {
	v, orgID, slug, err := a.transcriptOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, refuse(http.StatusBadRequest, "body.invalid", "Send the entry as JSON.")
	}
	e, created, err := a.agents.Append(ctx, v, orgID, req.Number, agents.Entry{Seq: req.Body.Seq, Kind: agents.EntryKind(req.Body.Kind), Body: req.Body.Body})
	if err != nil {
		return nil, fail(err, "task")
	}
	body, tag, err := transcriptEntry(e)
	if err != nil {
		return nil, err
	}
	if !created {
		return AppendTranscript200JSONResponse{Body: body, Headers: AppendTranscript200ResponseHeaders{ETag: tag}}, nil
	}
	return AppendTranscript201JSONResponse{Body: body, Headers: AppendTranscript201ResponseHeaders{ETag: tag,
		Location: transcriptPath(slug, req.Number, e.Seq)}}, nil
}

// GetTranscriptEntry answers one entry of a task's transcript by its number.
func (a api) GetTranscriptEntry(ctx context.Context, req GetTranscriptEntryRequestObject) (GetTranscriptEntryResponseObject, error) {
	v, orgID, _, err := a.transcriptOrg(ctx, req.Org)
	if err != nil {
		return nil, err
	}
	entries, err := a.agents.Transcript(ctx, v, orgID, req.Number, req.Seq-1, 1)
	if err != nil {
		return nil, fail(err, "task")
	}
	if len(entries) == 0 || entries[0].Seq != req.Seq {
		return nil, refuse(http.StatusNotFound, "transcript_entry.not_found", "The task's transcript has no such entry.")
	}
	body, tag, err := transcriptEntry(entries[0])
	if err != nil {
		return nil, err
	}
	if matches(req.Params.IfNoneMatch, tag) {
		return GetTranscriptEntry304Response{Headers: GetTranscriptEntry304ResponseHeaders{ETag: tag}}, nil
	}
	return GetTranscriptEntry200JSONResponse{Body: body, Headers: GetTranscriptEntry200ResponseHeaders{ETag: tag}}, nil
}
