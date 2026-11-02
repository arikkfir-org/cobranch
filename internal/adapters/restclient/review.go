package restclient

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/reviewer"
)

var _ reviewer.API = (*API)(nil)

// reviewsPage is how many reviews a read of a commit's reviews asks for at once: the most a page holds.
const reviewsPage = 100

// BranchDiff answers the branch's change as a request to merge into its base (reviewer.API).
func (c *API) BranchDiff(ctx context.Context, org, repo, branch string) (code.Diff, error) {
	resp, err := c.api.GetBranchDiffWithResponse(ctx, org, repo, branch, nil)
	if err != nil {
		return code.Diff{}, fmt.Errorf("reading branch %s's diff: %w", branch, err)
	}
	if resp.JSON200 == nil {
		return code.Diff{}, notFound(refused("reading branch "+branch+"'s diff", resp.StatusCode(), resp.Body), resp.StatusCode())
	}
	return diff(*resp.JSON200), nil
}

// File answers the file at path of the commit ref names (reviewer.API).
func (c *API) File(ctx context.Context, org, repo, ref, path string) (code.File, error) {
	resp, err := c.api.GetBlobWithResponse(ctx, org, repo, &GetBlobParams{Ref: &ref, Path: path})
	if err != nil {
		return code.File{}, fmt.Errorf("reading %s at %s: %w", path, ref, err)
	}
	if resp.JSON200 == nil {
		return code.File{}, notFound(refused("reading "+path+" at "+ref, resp.StatusCode(), resp.Body), resp.StatusCode())
	}
	f := resp.JSON200
	return code.File{Commit: f.Commit, Path: f.Path, Name: f.Name, Kind: code.EntryKind(f.Kind), Mode: f.Mode, SHA: f.Sha, Size: f.Size,
		Binary: f.Binary, Truncated: f.Truncated, Text: deref(f.Text, "")}, nil
}

// Tree answers the directory at path of the commit ref names (reviewer.API).
func (c *API) Tree(ctx context.Context, org, repo, ref, path string) (code.Tree, error) {
	params := &GetTreeParams{Ref: &ref}
	if path != "" {
		params.Path = &path
	}
	resp, err := c.api.GetTreeWithResponse(ctx, org, repo, params)
	if err != nil {
		return code.Tree{}, fmt.Errorf("listing %s at %s: %w", path, ref, err)
	}
	if resp.JSON200 == nil {
		return code.Tree{}, notFound(refused("listing "+path+" at "+ref, resp.StatusCode(), resp.Body), resp.StatusCode())
	}
	t := code.Tree{Commit: resp.JSON200.Commit, Path: resp.JSON200.Path, SHA: resp.JSON200.Sha}
	for _, e := range resp.JSON200.Entries {
		t.Entries = append(t.Entries, code.Entry{Name: e.Name, Path: e.Path, Kind: code.EntryKind(e.Kind), Mode: e.Mode, SHA: e.Sha,
			Size: deref(e.Size, 0)})
	}
	return t, nil
}

// Reviews answers every review of the commit, in the order they were submitted, a page at a time (reviewer.API).
func (c *API) Reviews(ctx context.Context, org, repo, sha string) ([]code.Review, error) {
	var out []code.Review
	params := &ListReviewsParams{Limit: new(reviewsPage)}
	for {
		resp, err := c.api.ListReviewsWithResponse(ctx, org, repo, sha, params)
		if err != nil {
			return nil, fmt.Errorf("reading %s's reviews: %w", sha, err)
		}
		if resp.JSON200 == nil {
			return nil, refused("reading "+sha+"'s reviews", resp.StatusCode(), resp.Body)
		}
		for _, r := range resp.JSON200.Reviews {
			out = append(out, review(r))
		}
		next := nextCursor(resp.HTTPResponse.Header.Get("Link"))
		if next == "" {
			return out, nil
		}
		params.Cursor = &next
	}
}

// SubmitReview submits the token's review of the commit (reviewer.API).
func (c *API) SubmitReview(ctx context.Context, org, repo, sha string, in code.ReviewInput) (code.Review, error) {
	body := ReviewInput{State: ReviewState(in.State), Body: &in.Body}
	if in.Branch != "" {
		body.Branch = &in.Branch
	}
	if len(in.Comments) > 0 {
		comments := make([]ReviewComment, 0, len(in.Comments))
		for _, cm := range in.Comments {
			a := cm.Anchor
			comments = append(comments, ReviewComment{Anchor: ChatAnchor{Sha: a.SHA, Path: a.Path, StartLine: a.StartLine, EndLine: a.EndLine}, Body: cm.Body})
		}
		body.Comments = &comments
	}
	resp, err := c.api.SubmitReviewWithResponse(ctx, org, repo, sha, body)
	if err != nil {
		return code.Review{}, fmt.Errorf("submitting a review of %s: %w", sha, err)
	}
	if resp.JSON201 == nil {
		return code.Review{}, refused("submitting a review of "+sha, resp.StatusCode(), resp.Body)
	}
	return review(*resp.JSON201), nil
}

// notFound is err, or reviewer.ErrNotFound beside it for a 404.
func notFound(err error, status int) error {
	if status == http.StatusNotFound {
		return fmt.Errorf("%w: %w", reviewer.ErrNotFound, err)
	}
	return err
}

// nextCursor is the cursor of the next page that a Link header names as rel="next", relative to the page; empty for
// none.
func nextCursor(link string) string {
	for _, l := range strings.Split(link, ",") {
		target, params, ok := strings.Cut(strings.TrimSpace(l), ";")
		if !ok || !strings.Contains(params, `rel="next"`) {
			continue
		}
		u, err := url.Parse(strings.Trim(strings.TrimSpace(target), "<>"))
		if err != nil {
			continue
		}
		return u.Query().Get("cursor")
	}
	return ""
}

// review is r as the code service names it.
func review(r Review) code.Review {
	out := code.Review{ID: r.Id, SHA: r.Sha, State: code.ReviewState(r.State), Body: r.Body, Person: r.Person, SubmittedAt: r.SubmittedAt}
	if r.Reviewer != nil {
		out.Reviewer = &code.Principal{ID: r.Reviewer.Id, DisplayName: r.Reviewer.DisplayName}
	}
	return out
}

// diff is d as the code service names it.
func diff(d Diff) code.Diff {
	out := code.Diff{Base: deref(d.Base, ""), BaseTip: deref(d.BaseTip, ""), Head: d.Head, FilesLeftOut: d.FilesLeftOut, Additions: d.Additions,
		Deletions: d.Deletions}
	for _, f := range d.Files {
		fd := code.FileDiff{Status: code.FileStatus(f.Status), Path: f.Path, OldPath: deref(f.OldPath, ""), OldMode: deref(f.OldMode, ""),
			Mode: deref(f.Mode, ""), Binary: f.Binary, Additions: f.Additions, Deletions: f.Deletions, Truncated: f.Truncated}
		for _, h := range f.Hunks {
			hunk := code.Hunk{OldStart: h.OldStart, OldLines: h.OldLines, NewStart: h.NewStart, NewLines: h.NewLines, Section: h.Section}
			for _, l := range h.Lines {
				hunk.Lines = append(hunk.Lines, code.DiffLine{Kind: code.LineKind(l.Kind), Old: deref(l.Old, 0), New: deref(l.New, 0), Text: l.Text,
					NoNewline: deref(l.NoNewline, false)})
			}
			fd.Hunks = append(fd.Hunks, hunk)
		}
		out.Files = append(out.Files, fd)
	}
	return out
}
