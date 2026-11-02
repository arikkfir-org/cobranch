package gitinternal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/system/telemetry"
)

// TimingName names each internal call in the Server-Timing header of the request that made it.
const TimingName = "git"

// Client reads a repository's trees, files and history from the Git server, and has it build merges, over the internal
// call: code.Objects. The REST and GraphQL servers each hold one, and it asks as their request's viewer: it sends the
// token the viewer's credential carried (auth.Token), or none for Anonymous, and the Git server decides what that
// viewer may read or merge.
type Client struct {
	base *url.URL
	http *http.Client
}

var _ code.Objects = (*Client)(nil)

// NewClient returns a client of the Git server's internal call at base, such as http://git-server-internal. Its
// requests continue the caller's trace; a request has no deadline but its context's, so a large file's bytes may
// stream for as long as their reader reads.
func NewClient(base *url.URL) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &Client{base: base, http: &http.Client{Transport: otelhttp.NewTransport(transport)}}
}

// Tree reads the directory at path in the commit ref names (code.Objects).
func (c *Client) Tree(ctx context.Context, repoID uuid.UUID, ref, path string) (code.Tree, error) {
	var tree Tree
	if err := c.getJSON(ctx, repoID, "tree", url.Values{"ref": {ref}, "path": {path}}, &tree); err != nil {
		return code.Tree{}, err
	}
	return tree.Domain(), nil
}

// Blob reads the file or symlink at path in the commit ref names, its content up to limit bytes (code.Objects).
func (c *Client) Blob(ctx context.Context, repoID uuid.UUID, ref, path string, limit int64) (code.Blob, error) {
	var blob Blob
	query := url.Values{"ref": {ref}, "path": {path}, "limit": {strconv.FormatInt(limit, 10)}}
	if err := c.getJSON(ctx, repoID, "blob", query, &blob); err != nil {
		return code.Blob{}, err
	}
	return blob.Domain(), nil
}

// Raw streams the bytes of the file or symlink at path in the commit ref names; the caller closes its Body
// (code.Objects).
func (c *Client) Raw(ctx context.Context, repoID uuid.UUID, ref, path string) (code.Raw, error) {
	resp, err := c.get(ctx, repoID, "raw", url.Values{"ref": {ref}, "path": {path}})
	if err != nil {
		return code.Raw{}, err
	}
	return code.Raw{Commit: resp.Header.Get(HeaderCommit), Path: path, SHA: resp.Header.Get(HeaderObject), Size: resp.ContentLength,
		Body: resp.Body}, nil
}

// Log lists at most limit of the commits reachable from the commit ref names, every parent followed, after the first
// skip (code.Objects).
func (c *Client) Log(ctx context.Context, repoID uuid.UUID, ref string, skip, limit int) (code.Log, error) {
	var log Log
	query := url.Values{"ref": {ref}, "skip": {strconv.Itoa(skip)}, "limit": {strconv.Itoa(limit)}}
	if err := c.getJSON(ctx, repoID, "log", query, &log); err != nil {
		return code.Log{}, err
	}
	return log.Domain(), nil
}

// Merge has the Git server build the merge req asks for and record it as a push of its base (code.Objects).
func (c *Client) Merge(ctx context.Context, repoID uuid.UUID, req code.MergeRequest) (code.Merged, error) {
	body, err := json.Marshal(MergeRequestOf(req))
	if err != nil {
		return code.Merged{}, fmt.Errorf("asking the Git server for a merge of repository %s: %w", repoID, err)
	}
	resp, err := c.do(ctx, http.MethodPost, repoID, "merge", nil, bytes.NewReader(body))
	if err != nil {
		return code.Merged{}, err
	}
	var merged Merged
	if err := readJSON(resp, &merged, "merge", repoID); err != nil {
		return code.Merged{}, err
	}
	return merged.Domain(), nil
}

// getJSON reads the answer of the internal call's read into out.
func (c *Client) getJSON(ctx context.Context, repoID uuid.UUID, read string, query url.Values, out any) error {
	resp, err := c.get(ctx, repoID, read, query)
	if err != nil {
		return err
	}
	return readJSON(resp, out, read, repoID)
}

// readJSON reads resp, the answer of the internal call's op, into out, and closes it.
func readJSON(resp *http.Response, out any, op string, repoID uuid.UUID) error {
	defer func() { _ = resp.Body.Close() }() // read whole; nothing left to tell
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("reading the Git server's %s of repository %s: %w", op, repoID, err)
	}
	return nil
}

// get asks the Git server for read of the repository.
func (c *Client) get(ctx context.Context, repoID uuid.UUID, read string, query url.Values) (*http.Response, error) {
	return c.do(ctx, http.MethodGet, repoID, read, query, nil)
}

// do asks the Git server for op of the repository, and returns its answer when it is 200; the domain's error when it
// is a 404 with one of answers' codes, a 403, or a 409 of a merge; or an error naming what it said. The request that
// asks names the call in its answer's Server-Timing, timed to the Git server's answer.
func (c *Client) do(ctx context.Context, method string, repoID uuid.UUID, op string, query url.Values, body io.Reader) (*http.Response, error) {
	defer telemetry.Time(ctx, TimingName)()
	u := c.base.JoinPath(ReposPath, repoID.String(), op)
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("asking the Git server for the %s of repository %s: %w", op, repoID, err)
	}
	if token, ok := auth.Token(ctx); ok {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("asking the Git server for the %s of repository %s: %w", op, repoID, err)
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	defer func() { _ = resp.Body.Close() }() // a problem's body is small; nothing left to tell
	var problem Problem
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&problem) // a body that isn't one leaves the status alone
	answer, ok := answers[problem.Code]
	switch {
	case ok && resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("reading the %s of repository %s: %w", op, repoID, answer)
	case resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("merging in repository %s: %w: %s", repoID, auth.ErrForbidden, problem.Message)
	case resp.StatusCode == http.StatusConflict && problem.Code == CodeMoved:
		return nil, fmt.Errorf("merging in repository %s: %w", repoID, code.ErrBranchMoved)
	case resp.StatusCode == http.StatusConflict && problem.Code != "":
		return nil, fmt.Errorf("merging in repository %s: %w", repoID, &code.MergeError{Code: problem.Code, Message: problem.Message})
	}
	return nil, fmt.Errorf("asking the Git server for the %s of repository %s: it answered %s: %s", op, repoID, resp.Status, problem.Message)
}
