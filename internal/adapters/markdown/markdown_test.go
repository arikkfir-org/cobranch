package markdown_test

import (
	"net/url"
	"strings"
	"testing"

	"cobranch.dev/internal/adapters/markdown"
	"cobranch.dev/internal/services/code"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// commit is the commit TestRender's document is at, doc the document and raw the start of its images' URLs.
var (
	commit = strings.Repeat("c", 40)
	doc    = code.Document{Org: "acme", Repo: "website", Ref: "feature/x", Commit: commit, Path: "docs/guide.md"}
	raw    = "https://api.example.com/v1/orgs/acme/repos/website/raw?path="
)

// renderCases are TestRender's: what a document's HTML must and mustn't hold.
var renderCases = []struct {
	name    string
	src     string
	want    []string
	wantNot []string
}{
	{name: "a heading's id is prefixed", src: "# Hello world\n", want: []string{`<h1 id="user-content-hello-world">Hello world</h1>`}},
	{name: "a table keeps its alignment", src: "| a | b |\n|:--|--:|\n| 1 | 2 |\n",
		want: []string{`<th align="left">a</th>`, `<td align="right">2</td>`}},
	{name: "a task list's boxes are disabled", src: "- [x] done\n- [ ] todo\n",
		want: []string{`<input checked="" type="checkbox" disabled=""/> done`, `<input type="checkbox" disabled=""/> todo`}},
	{name: "a code block keeps its language", src: "```go\npackage main\n```\n", want: []string{`<code class="language-go">package main`}},
	{name: "strikethrough and autolinks", src: "~~gone~~ www.example.com\n",
		want: []string{`<del>gone</del>`, `<a href="http://www.example.com" rel="nofollow">www.example.com</a>`}},
	{name: "a footnote's ids and links are prefixed", src: "See[^1].\n\n[^1]: There.\n",
		want: []string{`id="user-content-fnref:1"`, `href="#user-content-fn:1"`, `id="user-content-fn:1"`}},
	{name: "a relative link opens the Console's page at the ref", src: "[setup](setup.md#install)\n",
		want: []string{`href="https://console.example.com/acme/website/blob/feature/x/docs/setup.md#user-content-install"`}},
	{name: "a link from the root", src: "[root](/README.md)\n", want: []string{`href="https://console.example.com/acme/website/blob/feature/x/README.md"`}},
	{name: "a directory's link opens its tree", src: "[up](../)\n", want: []string{`href="https://console.example.com/acme/website/tree/feature/x"`}},
	{name: "a name with a space is escaped", src: "[it](<a b.md>)\n", want: []string{`href="https://console.example.com/acme/website/blob/feature/x/docs/a%20b.md"`}},
	{name: "a link above the root goes nowhere", src: "[out](../../x.md)\n", want: []string{`<p>out</p>`}},
	{name: "an anchor of the document", src: "[top](#hello)\n", want: []string{`href="#user-content-hello"`}},
	{name: "a URL elsewhere stays", src: "[site](https://example.org/a?b=c) <mailto:x@example.org>\n",
		want: []string{`href="https://example.org/a?b=c"`, `href="mailto:x@example.org"`}},
	{name: "a script URL goes", src: "[x](javascript:alert(1))\n", wantNot: []string{"javascript"}},
	{name: "a relative image loads the raw bytes at the commit", src: "![logo](../img/logo.png)\n",
		want: []string{`<img alt="logo" src="` + raw + `img%2Flogo.png&amp;ref=` + commit + `"/>`}},
	{name: "an image from elsewhere becomes a link", src: "![build](https://ci.example.org/badge.svg)\n",
		want: []string{`<a href="https://ci.example.org/badge.svg" rel="nofollow">build</a>`}, wantNot: []string{"<img"}},
	{name: "an HTML image from elsewhere too", src: `<img src="//cdn.example.org/x.png">` + "\n",
		want: []string{`<a href="https://cdn.example.org/x.png" rel="nofollow">https://cdn.example.org/x.png</a>`}, wantNot: []string{"<img"}},
	{name: "an inline PNG stays", src: `<img src="data:image/png;base64,iVBORw0KGgo=" width="20">` + "\n",
		want: []string{`src="data:image/png;base64,iVBORw0KGgo="`, `width="20"`}},
	{name: "an inline SVG goes", src: `<img src="data:image/svg+xml;base64,PHN2Zz4=">` + "\n", wantNot: []string{"<img", "svg"}},
	{name: "scripts, styles, frames and handlers go",
		src:     "<script>alert(1)</script>\n<style>p{}</style>\n<iframe src=\"https://example.org\"></iframe>\n<p onclick=\"x()\" style=\"color:red\">hi</p>\n",
		want:    []string{"<p>hi</p>"},
		wantNot: []string{"script", "alert", "<style", "iframe", "onclick", "color:red"}},
	{name: "forms and other inputs go", src: `<form action="https://example.org"><input type="text" name="q"><button>Go</button></form>` + "\n",
		wantNot: []string{"<form", "<input", "type=\"text\""}},
	{name: "details and summary stay", src: "<details open><summary>More</summary>\n\nHidden\n\n</details>\n",
		want: []string{`<details open="">`, `<summary>More</summary>`}},
	{name: "a raw HTML anchor's id and name are prefixed", src: `<a name="x" id="y">here</a>` + "\n",
		want: []string{`name="user-content-x"`, `id="user-content-y"`}},
	{name: "srcset goes", src: `<img src="a.png" srcset="https://example.org/b.png 2x">` + "\n", wantNot: []string{"srcset", "example.org"}},
}

func TestRender(t *testing.T) {
	r := markdown.New(mustURL(t, "https://console.example.com/"), mustURL(t, "https://api.example.com"))
	for _, tt := range renderCases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.Render(t.Context(), []byte(tt.src), doc)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("Render() = %s\nwant it to hold %s", got, w)
				}
			}
			for _, w := range tt.wantNot {
				if strings.Contains(got, w) {
					t.Errorf("Render() = %s\nwant it without %s", got, w)
				}
			}
		})
	}
}

func TestRenderWithoutURLs(t *testing.T) {
	r := markdown.New(nil, nil)
	got, err := r.Render(t.Context(), []byte("[a](a.md) ![b](b.png)\n"), code.Document{Org: "acme", Repo: "website", Ref: "main", Commit: "c1", Path: "README.md"})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{`href="/acme/website/blob/main/a.md"`, `src="/v1/orgs/acme/repos/website/raw?path=b.png&amp;ref=c1"`} {
		if !strings.Contains(got, w) {
			t.Errorf("Render() = %s\nwant it to hold %s", got, w)
		}
	}
}
