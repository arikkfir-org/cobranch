// Package markdown renders a repository's Markdown as GitHub-flavoured Markdown, into HTML that runs nothing and loads
// nothing from outside the installation but images from other hosts, which the viewer's browser loads: code.Renderer,
// which the code service renders Markdown files and READMEs with
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/code-browsing.md, "Markdown").
package markdown

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"cobranch.dev/internal/services/code"
)

var tracer = otel.Tracer("cobranch.dev/internal/adapters/markdown")

// idPrefix starts every id and name a document's HTML holds, so that no document clobbers its page's own, as GitHub's.
const idPrefix = "user-content-"

// dataImage matches the data URLs of the images a document may hold inline: PNG, JPEG, GIF and WebP.
var dataImage = regexp.MustCompile(`^data:image/(png|jpeg|gif|webp);base64,`)

// Renderer renders Markdown with goldmark and GitHub's extensions, keeps its raw HTML, points its relative links at
// the Console's pages and its relative images at the REST API's raw bytes, keeps images from elsewhere at their URLs,
// then sanitizes what is left with an allow-list in the spirit of GitHub's.
type Renderer struct {
	console string
	rest    string
	md      goldmark.Markdown
	policy  *bluemonday.Policy
}

// New returns a Renderer whose links reach the Console at console and whose images reach the REST API at rest, each
// without a trailing slash; a nil URL is the root of the page's own host.
func New(console, rest *url.URL) *Renderer {
	md := goldmark.New(
		goldmark.WithExtensions(extension.NewTable(extension.WithTableCellAlignMethod(extension.TableCellAlignAttribute)),
			extension.Strikethrough, extension.Linkify, extension.TaskList, extension.Footnote),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(gmhtml.WithUnsafe()),
	)
	return &Renderer{console: base(console), rest: base(rest), md: md, policy: policy()}
}

// base is u without a trailing slash, or "" for none.
func base(u *url.URL) string {
	if u == nil {
		return ""
	}
	return strings.TrimSuffix(u.String(), "/")
}

// policy is the allow-list a document's HTML keeps: bluemonday's for content people write, with code blocks'
// languages, alignments, details, a task list's checkboxes, images' sizes and inline images, and ids that start with
// idPrefix.
func policy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("id").Matching(regexp.MustCompile(`^` + idPrefix + `\S+$`)).Globally()
	p.AllowAttrs("name").Matching(regexp.MustCompile(`^` + idPrefix + `\S+$`)).OnElements("a")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^language-[\w+#.-]+$`)).OnElements("code")
	p.AllowAttrs("align").Matching(regexp.MustCompile(`^(?i)(left|right|center|justify)$`)).
		OnElements("p", "div", "h1", "h2", "h3", "h4", "h5", "h6", "img", "table", "th", "td", "tr")
	p.AllowElements("details", "summary", "kbd", "sup", "sub", "ins", "del", "s", "mark", "picture")
	p.AllowAttrs("open").OnElements("details")
	p.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
	p.AllowAttrs("checked", "disabled").OnElements("input")
	p.AllowAttrs("width", "height").Matching(regexp.MustCompile(`^\d+%?$`)).OnElements("img")
	p.AllowURLSchemes("http", "https", "mailto")
	p.AllowDataURIImages()
	return p
}

// Render renders src, the Markdown of the file doc names, as HTML (code.Renderer).
func (r *Renderer) Render(ctx context.Context, src []byte, doc code.Document) (string, error) {
	_, span := tracer.Start(ctx, "markdown.Renderer.Render")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.path", doc.Path), attribute.String("cobranch.commit", doc.Commit),
		attribute.Int("cobranch.markdown.size", len(src)))
	var out bytes.Buffer
	if err := r.md.Convert(src, &out); err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return "", fmt.Errorf("rendering the Markdown of %s: %w", doc.Path, err)
	}
	rewritten, err := r.rewrite(out.Bytes(), doc)
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		return "", err
	}
	return r.policy.Sanitize(rewritten), nil
}

// rewrite points the links and images of rendered, the HTML of doc's Markdown, where the Console reads them, prefixes
// its ids and names, and keeps only disabled checkboxes of its inputs.
func (r *Renderer) rewrite(rendered []byte, doc code.Document) (string, error) {
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(bytes.NewReader(rendered), body)
	if err != nil {
		return "", fmt.Errorf("parsing the HTML of %s: %w", doc.Path, err)
	}
	var out strings.Builder
	for _, n := range nodes {
		body.AppendChild(n)
	}
	r.walk(body, doc)
	for n := body.FirstChild; n != nil; n = n.NextSibling {
		if err := html.Render(&out, n); err != nil {
			return "", fmt.Errorf("writing the HTML of %s: %w", doc.Path, err)
		}
	}
	return out.String(), nil
}

// walk rewrites n's descendants in place.
func (r *Renderer) walk(n *html.Node, doc code.Document) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode {
			r.element(c, doc)
		}
		c = next
	}
}

// element rewrites e and its descendants, replacing or removing e where it can't stay as it is.
func (r *Renderer) element(e *html.Node, doc code.Document) {
	for i, a := range e.Attr {
		if (a.Key == "id" || a.Key == "name") && a.Val != "" && !strings.HasPrefix(a.Val, idPrefix) {
			e.Attr[i].Val = idPrefix + a.Val
		}
	}
	removeAttr(e, "srcset")
	switch e.DataAtom {
	case atom.A:
		if href, ok := attr(e, "href"); ok {
			setOrRemove(e, "href", r.link(href, doc))
		}
	case atom.Img:
		r.image(e, doc)
		return
	case atom.Input:
		if t, _ := attr(e, "type"); !strings.EqualFold(t, "checkbox") {
			e.Parent.RemoveChild(e)
			return
		}
		removeAttr(e, "disabled")
		e.Attr = append(e.Attr, html.Attribute{Key: "disabled"})
	}
	r.walk(e, doc)
}

// image points img's source at the REST API's raw bytes when it is in the repository, and keeps it when it is an
// inline image or from elsewhere.
func (r *Renderer) image(img *html.Node, doc code.Document) {
	src, _ := attr(img, "src")
	u, err := url.Parse(strings.TrimSpace(src))
	switch {
	case err != nil || src == "":
		img.Parent.RemoveChild(img)
	case strings.EqualFold(u.Scheme, "data"):
		if !dataImage.MatchString(src) {
			img.Parent.RemoveChild(img)
		}
	case u.Scheme != "" || u.Host != "":
		r.outside(img, u)
	default:
		p, ok := resolve(doc.Path, u.Path)
		if !ok || p == "" {
			img.Parent.RemoveChild(img)
			return
		}
		setOrRemove(img, "src", r.rest+"/v1/orgs/"+url.PathEscape(doc.Org)+"/repos/"+url.PathEscape(doc.Repo)+"/raw?"+
			url.Values{"ref": {doc.Commit}, "path": {p}}.Encode())
	}
}

// outside keeps img, an image from elsewhere, for the viewer's browser to load from its host, which says how long
// it may be kept: at https when its URL names a host alone; only an http or https URL stays.
func (r *Renderer) outside(img *html.Node, u *url.URL) {
	if u.Scheme == "" {
		u.Scheme = "https"
	}
	if scheme := strings.ToLower(u.Scheme); scheme != "http" && scheme != "https" {
		img.Parent.RemoveChild(img)
		return
	}
	setOrRemove(img, "src", u.String())
}

// link is where href, a link of doc, points: a URL of http, https or mailto as it is, at https when it names a host
// alone; an anchor of the document with idPrefix; a path in the repository, relative to doc or from the root, at the
// Console's page of it at doc's ref, its tree's when it ends in a slash; "" for anything else.
func (r *Renderer) link(href string, doc code.Document) string {
	u, err := url.Parse(strings.TrimSpace(href))
	switch {
	case err != nil:
		return ""
	case u.Scheme != "" || u.Host != "":
		if u.Scheme == "" {
			u.Scheme = "https"
		}
		if scheme := strings.ToLower(u.Scheme); scheme == "http" || scheme == "https" || scheme == "mailto" {
			return u.String()
		}
		return ""
	case u.Path == "":
		if u.Fragment == "" {
			return ""
		}
		return "#" + anchor(u.Fragment)
	}
	p, ok := resolve(doc.Path, u.Path)
	if !ok {
		return ""
	}
	kind := "blob"
	if p == "" || strings.HasSuffix(u.Path, "/") {
		kind = "tree"
	}
	out := r.console + "/" + url.PathEscape(doc.Org) + "/" + url.PathEscape(doc.Repo) + "/" + kind + "/" + escape(doc.Ref)
	if p != "" {
		out += "/" + escape(p)
	}
	if u.Fragment != "" {
		out += "#" + anchor(u.Fragment)
	}
	return out
}

// anchor is the id a fragment names in a document's HTML: with idPrefix.
func anchor(fragment string) string {
	if strings.HasPrefix(fragment, idPrefix) {
		return url.PathEscape(fragment)
	}
	return url.PathEscape(idPrefix + fragment)
}

// resolve is the repository path that ref, a link of the file at doc, names: from the repository's root when it
// starts with a slash, else from doc's directory; false when it climbs above the root.
func resolve(doc, ref string) (string, bool) {
	var parts []string
	if dir := path.Dir(doc); dir != "." && !strings.HasPrefix(ref, "/") {
		parts = strings.Split(dir, "/")
	}
	for _, part := range strings.Split(ref, "/") {
		switch part {
		case "", ".":
		case "..":
			if len(parts) == 0 {
				return "", false
			}
			parts = parts[:len(parts)-1]
		default:
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "/"), true
}

// escape escapes each of p's segments for a URL's path.
func escape(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// attr is the value of e's attribute key, and whether e has it.
func attr(e *html.Node, key string) (string, bool) {
	for _, a := range e.Attr {
		if a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

// setOrRemove sets e's attribute key to val, or removes it when val is "".
func setOrRemove(e *html.Node, key, val string) {
	removeAttr(e, key)
	if val != "" {
		e.Attr = append(e.Attr, html.Attribute{Key: key, Val: val})
	}
}

// removeAttr removes e's attribute key.
func removeAttr(e *html.Node, key string) {
	attrs := e.Attr[:0]
	for _, a := range e.Attr {
		if a.Key != key {
			attrs = append(attrs, a)
		}
	}
	e.Attr = attrs
}
