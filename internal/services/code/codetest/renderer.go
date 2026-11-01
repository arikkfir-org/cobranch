package codetest

import (
	"context"
	"fmt"

	"cobranch.dev/internal/services/code"
)

// Renderer is a code.Renderer that wraps a document's Markdown in a div that names where it is, so a test tells what
// rendered and at which ref, commit and path. Fail, when set, fails every render.
type Renderer struct {
	Fail error
}

// Render wraps src in a div whose data-doc is doc's org/repo@ref:commit:path.
func (r *Renderer) Render(_ context.Context, src []byte, doc code.Document) (string, error) {
	if r.Fail != nil {
		return "", r.Fail
	}
	return fmt.Sprintf(`<div data-doc="%s/%s@%s:%s:%s">%s</div>`, doc.Org, doc.Repo, doc.Ref, doc.Commit, doc.Path, src), nil
}
