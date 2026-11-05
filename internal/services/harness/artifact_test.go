package harness_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/harness"
)

// artifact is one of the organization's artifacts as the fake API holds it: its versions, oldest first, and its ETag,
// which every read moves on when bumpOnRead, as if someone changed it each time the task looked.
type artifact struct {
	harness.Artifact
	versions   []harness.ArtifactVersion
	bumpOnRead bool
}

func (c *cobranch) CreateArtifact(_ context.Context, org string, a harness.NewArtifact) (harness.Artifact, error) {
	if org != c.work.OrgSlug {
		return harness.Artifact{}, errors.New("another organization")
	}
	c.made = append(c.made, a)
	if c.artifacts == nil {
		c.artifacts = map[uuid.UUID]*artifact{}
	}
	if found, ok := c.artifacts[a.ID]; ok {
		return found.Artifact, nil
	}
	made := &artifact{Artifact: harness.Artifact{ID: a.ID, Title: a.Title, Kind: a.Kind, Repo: a.Repo, Head: 1, ETag: `"1"`},
		versions: []harness.ArtifactVersion{{ID: a.ID, Number: 1, Format: a.Format, Body: a.Body, Note: a.Note}}}
	c.artifacts[a.ID] = made
	return made.Artifact, nil
}

func (c *cobranch) Artifact(_ context.Context, org string, id uuid.UUID, n int) (harness.Artifact, error) {
	a, ok := c.artifacts[id]
	if org != c.work.OrgSlug || !ok {
		return harness.Artifact{}, errors.New("the API answered 404, artifact.not_found")
	}
	if n == 0 {
		n = a.Head
	}
	if n > len(a.versions) {
		return harness.Artifact{}, errors.New("the API answered 404, version.not_found")
	}
	out := a.Artifact
	out.Version = a.versions[n-1]
	if a.bumpOnRead {
		a.ETag = `"` + strconv.Itoa(len(c.made)+len(a.versions)+len(a.ETag)) + `"`
	}
	return out, nil
}

func (c *cobranch) AddArtifactVersion(_ context.Context, org string, id uuid.UUID, etag string, v harness.ArtifactVersion) (int, string, error) {
	a, ok := c.artifacts[id]
	if org != c.work.OrgSlug || !ok {
		return 0, "", errors.New("the API answered 404, artifact.not_found")
	}
	if i := slices.IndexFunc(a.versions, func(w harness.ArtifactVersion) bool { return w.ID == v.ID }); i >= 0 {
		return a.versions[i].Number, a.ETag, nil
	}
	if etag != a.ETag {
		return 0, "", harness.ErrArtifactChanged
	}
	a.Head++
	v.Number = a.Head
	a.versions = append(a.versions, v)
	a.ETag = `"` + strconv.Itoa(a.Head+10) + `"`
	return v.Number, a.ETag, nil
}

// planned is the organization's plan, a Markdown document at version 2, attached to website; it moves on each time the
// task reads it when bumping.
func planned(id uuid.UUID, bumping bool) map[uuid.UUID]*artifact {
	return map[uuid.UUID]*artifact{id: {Artifact: harness.Artifact{ID: id, Title: "Plan", Kind: "document", Repo: "website", Head: 2, ETag: `"4"`},
		versions: []harness.ArtifactVersion{{ID: uuid.New(), Number: 1, Format: "text/markdown", Body: "# Plan"},
			{ID: uuid.New(), Number: 2, Format: "text/markdown", Body: "# Plan\n\nRoll out.", Note: "Added the rollout"}}, bumpOnRead: bumping}}
}

// createArtifactCases are TestChat's cases of the artifacts a task writes.
func createArtifactCases() []chatCase {
	key := uuid.MustParse("0190f1b2-0000-7000-8000-0000000000c1")
	plan := call("a", "create_artifact", `{"title":"Plan","kind":"document","format":"markdown","body":"# Plan","note":"First"}`)
	return []chatCase{
		{name: "one asked for on a branch is its repository's, with its card", branch: "tidy", turns: []harness.Turn{calling(plan), done},
			check: func(t *testing.T, c *cobranch, m *model) {
				if len(c.made) != 1 {
					t.Fatalf("made %+v", c.made)
				}
				id := c.made[0].ID
				want := harness.NewArtifact{ID: id, Repo: "website", Branch: "tidy", Title: "Plan", Kind: "document", Format: "text/markdown", Body: "# Plan", Note: "First"}
				if c.made[0] != want || !slices.Equal(c.cards, []uuid.UUID{id}) || c.threads[id] == nil || c.threads[id].messages[0].Body != "Wrote document **Plan**." {
					t.Errorf("made %+v, posted cards %v in %+v", c.made, c.cards, c.threads)
				}
				resultAt(2, "Wrote artifact "+id.String()+", version 1, attached to repository website. It stays private", false)(t, c, nil, nil)
				containsAll(t, string(c.entries[2].Body), "Its card is in the task's thread.")
				prompts(t, m, []string{"write it as an artifact with create_artifact"}, nil)
			}},
		{name: "one asked for on an issue is the organization's", issue: 12, turns: []harness.Turn{calling(plan), done},
			check: func(t *testing.T, c *cobranch, _ *model) {
				if len(c.made) != 1 || c.made[0].Repo != "" || c.made[0].Branch != "" || !slices.Equal(c.cards, []uuid.UUID{c.made[0].ID}) {
					t.Fatalf("made %+v, posted cards %v", c.made, c.cards)
				}
				resultAt(2, "Wrote artifact "+c.made[0].ID.String()+", version 1, attached to no repository.", false)(t, c, nil, nil)
			}},
		{name: "a task without a chat posts no card", turns: []harness.Turn{calling(plan), done},
			check: func(t *testing.T, c *cobranch, _ *model) {
				if len(c.made) != 1 || c.made[0].Repo != "website" || len(c.cards) != 0 {
					t.Errorf("made %+v, posted cards %v", c.made, c.cards)
				}
				if body := string(c.entries[2].Body); strings.Contains(body, "card") {
					t.Errorf("create_artifact answered %s", body)
				}
			}},
		{name: "the call run again after a restart writes nothing more", branch: "tidy", thread: key, artifacts: planned(key, false),
			threads: map[uuid.UUID]*thread{key: {chat: harness.Chat{Repo: "website", Branch: "tidy"}, messages: []harness.ChatMessage{{ID: uuid.New(), Body: "Wrote document **Plan**.", Task: 7}}}},
			entries: entry(entry(nil, agents.EntryInput, `{"text":"# Sweep"}`), agents.EntryTurn, `{"calls":[{"id":"a","name":"create_artifact","arguments":`+
				string(plan.Arguments)+`,"key":"`+key.String()+`"}]}`),
			turns: []harness.Turn{done},
			check: func(t *testing.T, c *cobranch, _ *model) {
				if len(c.made) != 1 || c.made[0].ID != key || len(c.artifacts) != 1 || len(c.artifacts[key].versions) != 2 || len(c.threads[key].messages) != 1 ||
					len(c.cards) != 0 {
					t.Errorf("made %+v, holds %+v, posted cards %v in %+v", c.made, c.artifacts, c.cards, c.threads)
				}
			}},
		{name: "a format it doesn't take", branch: "tidy",
			turns: []harness.Turn{calling(call("a", "create_artifact", `{"title":"Plan","kind":"document","format":"pdf","body":"x"}`)), done},
			check: resultAtChat(2, `create_artifact failed: format "pdf" is none of markdown, svg and html`, true)},
	}
}

// changeArtifactCases are TestChat's cases of a task reading an artifact and adding its versions.
func changeArtifactCases() []chatCase {
	planID := uuid.MustParse("0190f1b2-0000-7000-8000-0000000000c2")
	read := call("a", "read_artifact", `{"id":"`+planID.String()+`"}`)
	update := call("b", "update_artifact", `{"id":"`+planID.String()+`","body":"# Plan\n\nRoll out slowly.","note":"Slower"}`)
	return []chatCase{
		{name: "read one, then add a version from it", branch: "tidy", artifacts: planned(planID, false), turns: []harness.Turn{calling(read), calling(update), done},
			check: func(t *testing.T, c *cobranch, _ *model) {
				resultAt(2, "Artifact "+planID.String()+`, "Plan", a document attached to repository website: version 2 of 2, in markdown (Added the rollout):`,
					false)(t, c, nil, nil)
				containsAll(t, string(c.entries[2].Body), "Roll out.")
				resultAt(4, "Added version 3 of artifact "+planID.String()+". Its card is in the task's thread.", false)(t, c, nil, nil)
				got := c.artifacts[planID].versions
				if len(got) != 3 || got[2].Format != "text/markdown" || got[2].Note != "Slower" || !slices.Equal(c.cards, []uuid.UUID{planID}) ||
					c.threads[got[2].ID] == nil || c.threads[got[2].ID].messages[0].Body != "Revised **Plan**: version 3. Slower" {
					t.Errorf("versions %+v, cards %v, threads %+v", got, c.cards, c.threads)
				}
			}},
		{name: "an older version", branch: "tidy", artifacts: planned(planID, false),
			turns: []harness.Turn{calling(call("a", "read_artifact", `{"id":"`+planID.String()+`","version":1}`)), done},
			check: resultAtChat(2, "Artifact "+planID.String()+`, "Plan", a document attached to repository website: version 1 of 2, in markdown:`, false)},
		{name: "a version someone added since the task read it refuses its own", branch: "tidy", artifacts: planned(planID, true),
			turns: []harness.Turn{calling(read), calling(update), done},
			check: func(t *testing.T, c *cobranch, _ *model) {
				resultAt(4, "update_artifact failed: someone changed the artifact since you read it", true)(t, c, nil, nil)
				if len(c.artifacts[planID].versions) != 2 || len(c.cards) != 0 {
					t.Errorf("versions %+v, cards %v", c.artifacts[planID].versions, c.cards)
				}
			}},
		{name: "a version made without reading it first is made from it as it is", artifacts: planned(planID, false), turns: []harness.Turn{calling(update), done},
			check: resultAtChat(2, "Added version 3 of artifact "+planID.String()+".", false)},
		{name: "one that isn't there", branch: "tidy", turns: []harness.Turn{calling(read), done},
			check: resultAtChat(2, "read_artifact failed: reading the artifact: the API answered 404", true)},
		{name: "an ID that isn't one", branch: "tidy", turns: []harness.Turn{calling(call("a", "read_artifact", `{"id":"plan"}`)), done},
			check: resultAtChat(2, `read_artifact failed: "plan" is no artifact's ID`, true)},
	}
}

// resultAtChat is resultAt as a chat case checks it.
func resultAtChat(i int, text string, failed bool) func(t *testing.T, c *cobranch, _ *model) {
	return func(t *testing.T, c *cobranch, _ *model) {
		t.Helper()
		resultAt(i, text, failed)(t, c, nil, nil)
	}
}
