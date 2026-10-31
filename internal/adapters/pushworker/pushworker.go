// Package pushworker carries a push's manifest from the Git server to the push worker: the push worker's one internal
// call, POST /v1/pushes, behind a bearer token only the two hold, and the Git server's client of it, which asks again
// with the same push ID when it lost the answer
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/git-hosting.md, "A push").
package pushworker

import (
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/git"
)

// Path is where the push worker takes pushes.
const Path = "/v1/pushes"

// maxManifest bounds a manifest's size: a first push of a long history lists every commit.
const maxManifest = 1 << 30

// pushJSON is a push's manifest on the wire.
type pushJSON struct {
	ID         uuid.UUID       `json:"id"`
	RepoID     uuid.UUID       `json:"repoId"`
	PusherID   uuid.UUID       `json:"pusherId"`
	Transport  git.Transport   `json:"transport"`
	Updates    []refUpdateJSON `json:"updates"`
	Pack       *packJSON       `json:"pack,omitempty"`
	Commits    []commitJSON    `json:"commits"`
	ReceivedAt time.Time       `json:"receivedAt"`
}

type refUpdateJSON struct {
	Name string `json:"name"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

type packJSON struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Objects int64  `json:"objects"`
}

type personJSON struct {
	Name  string    `json:"name"`
	Email string    `json:"email"`
	At    time.Time `json:"at"`
	Zone  string    `json:"zone"`
}

type commitJSON struct {
	SHA       string     `json:"sha"`
	Tree      string     `json:"tree"`
	Parents   []string   `json:"parents"`
	Author    personJSON `json:"author"`
	Committer personJSON `json:"committer"`
	Message   string     `json:"message"`
	Signature string     `json:"signature,omitempty"`
}

// answerJSON is the push worker's answer on the wire.
type answerJSON struct {
	Accepted bool   `json:"accepted"`
	Message  string `json:"message,omitempty"`
}

func toWire(p git.Push) pushJSON {
	out := pushJSON{ID: p.ID, RepoID: p.RepoID, PusherID: p.PusherID, Transport: p.Transport, ReceivedAt: p.ReceivedAt,
		Updates: make([]refUpdateJSON, 0, len(p.Updates)), Commits: make([]commitJSON, 0, len(p.Commits))}
	for _, u := range p.Updates {
		out.Updates = append(out.Updates, refUpdateJSON(u))
	}
	if p.Pack != nil {
		pack := packJSON(*p.Pack)
		out.Pack = &pack
	}
	for _, c := range p.Commits {
		out.Commits = append(out.Commits, commitJSON{SHA: c.SHA, Tree: c.Tree, Parents: c.Parents, Author: personJSON(c.Author),
			Committer: personJSON(c.Committer), Message: c.Message, Signature: c.Signature})
	}
	return out
}

func fromWire(p pushJSON) git.Push {
	out := git.Push{ID: p.ID, RepoID: p.RepoID, PusherID: p.PusherID, Transport: p.Transport, ReceivedAt: p.ReceivedAt}
	for _, u := range p.Updates {
		out.Updates = append(out.Updates, git.RefUpdate(u))
	}
	if p.Pack != nil {
		pack := git.Pack(*p.Pack)
		out.Pack = &pack
	}
	for _, c := range p.Commits {
		out.Commits = append(out.Commits, git.Commit{SHA: c.SHA, Tree: c.Tree, Parents: c.Parents, Author: git.Person(c.Author),
			Committer: git.Person(c.Committer), Message: c.Message, Signature: c.Signature})
	}
	return out
}
