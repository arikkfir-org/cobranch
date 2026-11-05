package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
)

// The artifact tools, which the harness runs itself through Cobranch's API: an artifact is a document, a diagram, a
// report or a page that the agent writes when asked, which belongs to whoever asked for it and stays private to them
// until they share it (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/artifacts.md).
var (
	createArtifactSpec = Spec{
		Name: "create_artifact",
		Description: "Writes an artifact that someone asked for: a document, a diagram, a report or a page, in Markdown, " +
			"SVG or HTML, kept in versions. It belongs to whoever assigned the task, who alone reads it until they share " +
			"it, and the task's thread shows it as a card that opens it. Scripts in SVG or HTML never run.",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"title":{"type":"string","description":"Its title, at most 200 characters"},` +
			`"summary":{"type":"string","description":"What it is, at most 500 characters"},` +
			`"kind":{"type":"string","enum":["document","diagram","report","page"]},` +
			`"format":{"type":"string","enum":["markdown","svg","html"]},` +
			`"body":{"type":"string","description":"Its content in that format, at most 1 MiB"},` +
			`"note":{"type":"string","description":"Why it was written, at most 500 characters"}},` +
			`"required":["title","kind","format","body"]}`),
	}
	readArtifactSpec = Spec{
		Name:        "read_artifact",
		Description: "Reads one of the organization's artifacts that you may read: its title, kind and a version's content, its newest unless you name one.",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"id":{"type":"string","description":"The artifact's ID"},` +
			`"version":{"type":"integer","minimum":1,"description":"The version's number; its newest when absent"}},` +
			`"required":["id"]}`),
	}
	updateArtifactSpec = Spec{
		Name: "update_artifact",
		Description: "Adds the next version of an artifact you may edit, with its whole new content, which becomes the one " +
			"it shows; earlier versions stay. Read it with read_artifact first: a version someone added since you read it " +
			"refuses yours. The task's thread shows the new version as a card.",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"id":{"type":"string","description":"The artifact's ID"},` +
			`"format":{"type":"string","enum":["markdown","svg","html"],"description":"Its newest version's format when absent"},` +
			`"body":{"type":"string","description":"Its whole new content, at most 1 MiB"},` +
			`"note":{"type":"string","description":"What changed, at most 500 characters"}},` +
			`"required":["id","body"]}`),
	}
)

// formats are the media types of the formats the tools take.
var formats = map[string]string{"markdown": "text/markdown", "svg": "image/svg+xml", "html": "text/html"}

// formatOf is the format a media type is, as the tools name it; the media type itself for one they don't.
func formatOf(mediaType string) string {
	for name, mt := range formats {
		if mt == mediaType {
			return name
		}
	}
	return mediaType
}

// artifactTools are the artifact tools; none for a task its kind keeps quiet.
func (r *run) artifactTools() []Tool {
	if r.kit.Quiet {
		return nil
	}
	return []Tool{chatTool{spec: createArtifactSpec, run: r.createArtifact}, chatTool{spec: readArtifactSpec, run: r.readArtifact},
		chatTool{spec: updateArtifactSpec, run: r.updateArtifact}}
}

// artifactRepo is the repository the task's artifacts are attached to: its own, unless it talks in its issue's chat,
// whose artifacts are the organization's, attached to none.
func (r *run) artifactRepo() string {
	t := r.work.Task
	if t.Branch == "" && r.work.IssueNumber != 0 {
		return ""
	}
	return t.RepoSlug
}

// createArgs are create_artifact's arguments.
type createArgs struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Kind    string `json:"kind"`
	Format  string `json:"format"`
	Body    string `json:"body"`
	Note    string `json:"note"`
}

// createArtifact writes the artifact, named by the call's key so that the call run again writes nothing more, and
// posts its card to the task's thread.
func (r *run) createArtifact(ctx context.Context, c Call) (string, error) {
	var a createArgs
	if err := decode(c.Arguments, &a); err != nil {
		return "", err
	}
	mediaType, ok := formats[a.Format]
	if !ok {
		return "", fmt.Errorf("format %q is none of markdown, svg and html", a.Format)
	}
	id, err := uuid.Parse(c.Key)
	if err != nil {
		return "", fmt.Errorf("the call has no key to name its artifact: %w", err)
	}
	repo := r.artifactRepo()
	branch := ""
	if repo != "" {
		branch = r.work.Task.Branch
	}
	made, err := r.cobranch.CreateArtifact(ctx, r.work.OrgSlug, NewArtifact{ID: id, Repo: repo, Branch: branch, Title: a.Title, Summary: a.Summary,
		Kind: a.Kind, Format: mediaType, Body: a.Body, Note: a.Note})
	if err != nil {
		return "", fmt.Errorf("writing the artifact: %w", err)
	}
	r.artifactTags[made.ID] = made.ETag
	out := "Wrote artifact " + made.ID.String() + ", version 1, " + attached(made.Repo) + ". It stays private to whoever assigned the task until they share it."
	posted, err := r.card(ctx, c.Key, "Wrote "+a.Kind+" **"+made.Title+"**.", made.ID)
	return out + posted, err
}

// attached says which repository an artifact is attached to.
func attached(repo string) string {
	if repo == "" {
		return "attached to no repository"
	}
	return "attached to repository " + repo
}

// card posts a message naming the artifact to the task's thread, under the call's key, and says so; nothing for a task
// without a chat.
func (r *run) card(ctx context.Context, key, body string, artifact uuid.UUID) (string, error) {
	if _, ok := r.chat(); !ok {
		return "", nil
	}
	if _, err := r.post(ctx, key, body, artifact); err != nil {
		return "", fmt.Errorf("posting its card: %w", err)
	}
	return " Its card is in the task's thread.", nil
}

// readArgs are read_artifact's arguments.
type readArgs struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

// readArtifact answers a version of the artifact with its content, and keeps the artifact's ETag for a version made
// from it.
func (r *run) readArtifact(ctx context.Context, c Call) (string, error) {
	var a readArgs
	if err := decode(c.Arguments, &a); err != nil {
		return "", err
	}
	id, err := uuid.Parse(a.ID)
	if err != nil {
		return "", fmt.Errorf("%q is no artifact's ID: %w", a.ID, err)
	}
	read, err := r.cobranch.Artifact(ctx, r.work.OrgSlug, id, a.Version)
	if err != nil {
		return "", fmt.Errorf("reading the artifact: %w", err)
	}
	r.artifactTags[read.ID] = read.ETag
	v := read.Version
	head := "Artifact " + read.ID.String() + ", \"" + read.Title + "\", a " + read.Kind + " " + attached(read.Repo) + ": version " +
		strconv.Itoa(v.Number) + " of " + strconv.Itoa(read.Head) + ", in " + formatOf(v.Format)
	if v.Note != "" {
		head += " (" + v.Note + ")"
	}
	head += ":\n\n"
	return head + cut(v.Body, MaxOutput-len(head)), nil
}

// updateArgs are update_artifact's arguments.
type updateArgs struct {
	ID     string `json:"id"`
	Format string `json:"format"`
	Body   string `json:"body"`
	Note   string `json:"note"`
}

// updateArtifact adds the artifact's next version, named by the call's key, made from the artifact as the task last
// read it, or as it is now when the task hasn't; and posts its card to the task's thread.
func (r *run) updateArtifact(ctx context.Context, c Call) (string, error) {
	var a updateArgs
	if err := decode(c.Arguments, &a); err != nil {
		return "", err
	}
	id, err := uuid.Parse(a.ID)
	if err != nil {
		return "", fmt.Errorf("%q is no artifact's ID: %w", a.ID, err)
	}
	key, err := uuid.Parse(c.Key)
	if err != nil {
		return "", fmt.Errorf("the call has no key to name its version: %w", err)
	}
	now, err := r.cobranch.Artifact(ctx, r.work.OrgSlug, id, 0)
	if err != nil {
		return "", fmt.Errorf("reading the artifact: %w", err)
	}
	mediaType := now.Version.Format
	if a.Format != "" {
		if mediaType = formats[a.Format]; mediaType == "" {
			return "", fmt.Errorf("format %q is none of markdown, svg and html", a.Format)
		}
	}
	tag, read := r.artifactTags[id]
	if !read {
		tag = now.ETag
	}
	number, tag, err := r.cobranch.AddArtifactVersion(ctx, r.work.OrgSlug, id, tag, ArtifactVersion{ID: key, Format: mediaType, Body: a.Body, Note: a.Note})
	if errors.Is(err, ErrArtifactChanged) {
		return "", errors.New("someone changed the artifact since you read it: read it again with read_artifact, and make your version from that")
	}
	if err != nil {
		return "", fmt.Errorf("adding the version: %w", err)
	}
	r.artifactTags[id] = tag
	out := "Added version " + strconv.Itoa(number) + " of artifact " + id.String() + "."
	body := "Revised **" + now.Title + "**: version " + strconv.Itoa(number) + "."
	if a.Note != "" {
		body += " " + a.Note
	}
	posted, err := r.card(ctx, c.Key, body, id)
	return out + posted, err
}
