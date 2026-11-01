package rest

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/git"
	"cobranch.dev/internal/services/repos"
)

// shape is the version of the shape of a tree's and a file's answers, which their ETags hold beside the commit and the
// path: a change of what the API answers for them bumps it, so no client keeps an answer of the old shape.
const shape = "2"

// rawPolicy is raw bytes' Content Security Policy: they load images of their own origin and data URLs alone, and run
// nothing, so an HTML or SVG file opened on the API's origin, which holds the session cookie, can't act for its
// visitor.
const rawPolicy = "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; sandbox"

// imageTypes are the media types of the images raw bytes are served as, by their file's extension.
var imageTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".avif": "image/avif", ".bmp": "image/bmp", ".ico": "image/x-icon", ".svg": "image/svg+xml",
}

// codeFail is fail for the code's reads: 404 with the code of what a ref or a path didn't find, and 503 without a Git
// server.
func codeFail(err error) error {
	switch {
	case errors.Is(err, code.ErrRefNotFound):
		return refuse(http.StatusNotFound, "ref.not_found", "No branch, tag or commit of the repository has this name.")
	case errors.Is(err, code.ErrPathNotFound):
		return refuse(http.StatusNotFound, "path.not_found", "The commit holds nothing at this path.")
	case errors.Is(err, code.ErrNotTree):
		return refuse(http.StatusNotFound, "path.file", "The path holds a file, not a directory.")
	case errors.Is(err, code.ErrNotBlob):
		return refuse(http.StatusNotFound, "path.directory", "The path holds a directory, not a file.")
	case errors.Is(err, code.ErrSubmodule):
		return refuse(http.StatusNotFound, "path.submodule", "The path holds a submodule, whose files are another repository's.")
	case errors.Is(err, code.ErrUnavailable):
		return refuse(http.StatusServiceUnavailable, "git.unavailable", "This installation runs no Git server to read files with.")
	}
	return fail(err, "repository")
}

// etag is the strong entity tag of an answer of kind that the commit and the path fix.
func etag(kind, commit, path string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + shape + "\x00" + commit + "\x00" + path))
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// opaque is an entity tag's opaque part, without its quotes or a weak prefix: If-None-Match compares weakly.
func opaque(tag string) string {
	return strings.Trim(strings.TrimPrefix(strings.TrimSpace(tag), "W/"), `"`)
}

// matches reports whether an If-None-Match header names tag: as one of its list, or as *.
func matches(header *IfNoneMatch, tag string) bool {
	if header == nil {
		return false
	}
	for t := range strings.SplitSeq(*header, ",") {
		if t = strings.TrimSpace(t); t == "*" || opaque(t) == opaque(tag) {
			return true
		}
	}
	return false
}

// refOf is the ref a request names, main when it names none.
func refOf(ref *Ref) string {
	if ref == nil {
		return git.DefaultBranch
	}
	return *ref
}

// ListTags lists the repository's tags.
func (a api) ListTags(ctx context.Context, req ListTagsRequestObject) (ListTagsResponseObject, error) {
	v, repo, err := a.viewerAndRepo(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	tags, err := a.code.Tags(ctx, v, repo.ID)
	if err != nil {
		return nil, fail(err, "repository")
	}
	return ListTags200JSONResponse{Tags: mapAll(tags, func(t code.Tag) Tag { return Tag{Name: t.Name, Target: t.Target} })}, nil
}

// GetTree answers the directory at a path of the commit a ref names, or 304 while the caller holds it.
func (a api) GetTree(ctx context.Context, req GetTreeRequestObject) (GetTreeResponseObject, error) {
	v, repo, err := a.viewerAndRepo(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	dir := ""
	if req.Params.Path != nil {
		dir = *req.Params.Path
	}
	tree, err := a.code.Tree(ctx, v, repo.ID, refOf(req.Params.Ref), dir)
	if err != nil {
		return nil, codeFail(err)
	}
	tag := etag("tree", tree.Commit, tree.Path)
	if matches(req.Params.IfNoneMatch, tag) {
		return GetTree304Response{Headers: GetTree304ResponseHeaders{ETag: tag}}, nil
	}
	body := Tree{Commit: tree.Commit, Path: tree.Path, Sha: tree.SHA, Entries: mapAll(tree.Entries, entry)}
	if tree.README != nil {
		readme := fileOf(*tree.README)
		body.Readme = &readme
	}
	return GetTree200JSONResponse{Body: body, Headers: GetTree200ResponseHeaders{ETag: tag}}, nil
}

func entry(e code.Entry) TreeEntry {
	out := TreeEntry{Name: e.Name, Path: e.Path, Kind: TreeEntryKind(e.Kind), Mode: e.Mode, Sha: e.SHA}
	if e.Kind == code.KindBlob || e.Kind == code.KindSymlink {
		size := e.Size
		out.Size = &size
	}
	return out
}

// GetBlob answers the file or symlink at a path of the commit a ref names, or 304 while the caller holds it.
func (a api) GetBlob(ctx context.Context, req GetBlobRequestObject) (GetBlobResponseObject, error) {
	v, repo, err := a.viewerAndRepo(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	f, err := a.code.File(ctx, v, repo.ID, refOf(req.Params.Ref), req.Params.Path)
	if err != nil {
		return nil, codeFail(err)
	}
	tag := etag("blob", f.Commit, f.Path)
	if matches(req.Params.IfNoneMatch, tag) {
		return GetBlob304Response{Headers: GetBlob304ResponseHeaders{ETag: tag}}, nil
	}
	return GetBlob200JSONResponse{Body: fileOf(f), Headers: GetBlob200ResponseHeaders{ETag: tag}}, nil
}

// fileOf is f on the wire: its text unless it is binary, and its HTML when it rendered.
func fileOf(f code.File) File {
	out := File{Commit: f.Commit, Path: f.Path, Name: f.Name, Kind: FileKind(f.Kind), Mode: f.Mode, Sha: f.SHA, Size: f.Size,
		Binary: f.Binary, Truncated: f.Truncated}
	if !f.Binary {
		out.Text = &f.Text
	}
	if f.HTML != "" {
		out.Html = &f.HTML
	}
	return out
}

// GetRaw streams the bytes of the file or symlink at a path of the commit a ref names, as nothing that runs, or
// answers 304 while the caller holds them: their ETag is their blob's object, which their content fixes. Asked at the
// full name of the commit it resolves to, as a Markdown document's images are, they never change, so the browser
// keeps them (rawCache).
func (a api) GetRaw(ctx context.Context, req GetRawRequestObject) (GetRawResponseObject, error) {
	v, repo, err := a.viewerAndRepo(ctx, req.Org, req.Repo)
	if err != nil {
		return nil, err
	}
	raw, err := a.code.Raw(ctx, v, repo.ID, refOf(req.Params.Ref), req.Params.Path)
	if err != nil {
		return nil, codeFail(err)
	}
	tag, cache := `"`+raw.SHA+`"`, rawCache(refOf(req.Params.Ref), raw.Commit)
	if matches(req.Params.IfNoneMatch, tag) {
		_ = raw.Body.Close() // nothing was read, and nothing will be
		return GetRaw304Response{Headers: GetRaw304ResponseHeaders{ETag: tag, CacheControl: cache}}, nil
	}
	body := bufio.NewReaderSize(raw.Body, 8192)
	contentType := rawType(raw, body)
	headers := GetRaw200ResponseHeaders{ETag: tag, CacheControl: cache, ContentSecurityPolicy: rawPolicy, XContentTypeOptions: "nosniff",
		CrossOriginResourcePolicy: "same-site"}
	if contentType == "application/octet-stream" || (req.Params.Download != nil && *req.Params.Download) {
		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(raw.Path)})
		headers.ContentDisposition = &disposition
	}
	return GetRaw200AsteriskResponse{Body: readCloser{Reader: body, Closer: raw.Body}, Headers: headers, ContentType: contentType, ContentLength: raw.Size}, nil
}

// rawCache is the Cache-Control of raw bytes asked at ref, which resolved to commit: kept for a year when ref is the
// commit's full name, since a commit's bytes never change; revalidated each time, as every answer, when it is a
// branch or a tag, even one named like a commit.
func rawCache(ref, commit string) string {
	if commit != "" && strings.EqualFold(ref, commit) {
		return "private, max-age=31536000, immutable"
	}
	return "private, no-cache"
}

// readCloser reads from Reader and closes Closer.
type readCloser struct {
	io.Reader
	io.Closer
}

// rawType is the media type raw's bytes are served as: an image's type by its file's extension; text/plain when
// their first bytes, which it peeks from body, are text as code.Binary judges it; or application/octet-stream.
func rawType(raw code.Raw, body *bufio.Reader) string {
	if t, ok := imageTypes[strings.ToLower(path.Ext(raw.Path))]; ok {
		return t
	}
	probe, _ := body.Peek(8000) // a shorter file peeks whole, and a failed read shows as the body's
	if code.Binary(probe, int64(len(probe)) < raw.Size) {
		return "application/octet-stream"
	}
	return "text/plain; charset=utf-8"
}

// viewerAndRepo is the request's viewer and the repository whose code the path names, which they read.
func (a api) viewerAndRepo(ctx context.Context, orgSlug, slug string) (repos.Viewer, repos.Repository, error) {
	v, err := viewer(ctx)
	if err != nil {
		return repos.Viewer{}, repos.Repository{}, err
	}
	repo, err := a.codeRepo(ctx, v, orgSlug, slug)
	if err != nil {
		return repos.Viewer{}, repos.Repository{}, err
	}
	return v, repo, nil
}
