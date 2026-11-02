package rest

import (
	"context"
	"errors"

	"cobranch.dev/internal/services/code"
)

// GetBranchDiff answers a branch's change as a request to merge into its base, or 304 while the caller holds it.
func (a api) GetBranchDiff(ctx context.Context, req GetBranchDiffRequestObject) (GetBranchDiffResponseObject, error) {
	v, repo, err := a.viewerAndRepo(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	d, err := a.code.BranchDiff(ctx, v, repo.ID, req.BranchName)
	if err != nil {
		return nil, diffFail(err, "branch")
	}
	tag := diffTag(d)
	if matches(req.Params.IfNoneMatch, tag) {
		return GetBranchDiff304Response{Headers: GetBranchDiff304ResponseHeaders{ETag: tag}}, nil
	}
	return GetBranchDiff200JSONResponse{Body: diff(d), Headers: GetBranchDiff200ResponseHeaders{ETag: tag}}, nil
}

// GetCommitDiff answers a commit's change against its first parent, or 304 while the caller holds it.
func (a api) GetCommitDiff(ctx context.Context, req GetCommitDiffRequestObject) (GetCommitDiffResponseObject, error) {
	v, repo, err := a.viewerAndRepo(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	d, err := a.code.CommitDiff(ctx, v, repo.ID, req.SHA)
	if err != nil {
		return nil, diffFail(err, "commit")
	}
	tag := diffTag(d)
	if matches(req.Params.IfNoneMatch, tag) {
		return GetCommitDiff304Response{Headers: GetCommitDiff304ResponseHeaders{ETag: tag}}, nil
	}
	return GetCommitDiff200JSONResponse{Body: diff(d), Headers: GetCommitDiff200ResponseHeaders{ETag: tag}}, nil
}

// diffFail is fail for a diff of kind: the code's reads' answers for a ref Git doesn't hold or a missing Git server,
// and the API's for the rest.
func diffFail(err error, kind string) error {
	if errors.Is(err, code.ErrRefNotFound) || errors.Is(err, code.ErrUnavailable) {
		return codeFail(err)
	}
	return fail(err, kind)
}

// diffTag is a diff's entity tag: the commits it runs between fix it, and nothing of the request, since it renders
// no links.
func diffTag(d code.Diff) string {
	return etag("diff", d.Head, d.Base, d.BaseTip, "", "")
}

// diff is d as the API shows it.
func diff(d code.Diff) Diff {
	return Diff{Base: optional(d.Base), BaseTip: optional(d.BaseTip), Head: d.Head, Files: mapAll(d.Files, fileDiff),
		FilesLeftOut: d.FilesLeftOut, Additions: d.Additions, Deletions: d.Deletions}
}

func fileDiff(f code.FileDiff) FileDiff {
	return FileDiff{Status: FileDiffStatus(f.Status), Path: f.Path, OldPath: optional(f.OldPath), OldMode: optional(f.OldMode),
		Mode: optional(f.Mode), Binary: f.Binary, Additions: f.Additions, Deletions: f.Deletions, Truncated: f.Truncated,
		Hunks: mapAll(f.Hunks, hunk)}
}

func hunk(h code.Hunk) Hunk {
	return Hunk{OldStart: h.OldStart, OldLines: h.OldLines, NewStart: h.NewStart, NewLines: h.NewLines, Section: h.Section,
		Lines: mapAll(h.Lines, diffLine)}
}

func diffLine(l code.DiffLine) DiffLine {
	out := DiffLine{Kind: DiffLineKind(l.Kind), Text: l.Text}
	if l.Kind != code.LineAdded {
		out.Old = &l.Old
	}
	if l.Kind != code.LineRemoved {
		out.New = &l.New
	}
	if l.NoNewline {
		out.NoNewline = &l.NoNewline
	}
	return out
}

// optional is s, or nil when it is empty.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
