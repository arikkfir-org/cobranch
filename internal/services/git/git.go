// Package git is the domain of Git hosting: a repository's refs and packs, the pushes that move them, the commits they
// bring and the principals behind those, and which pushes Cobranch accepts. It knows nothing of Git's protocol, the
// git CLI, HTTP or PostgreSQL: the Git server reads snapshots and records push manifests, and the push worker
// processes them, through its port, Store
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/git-hosting.md, "A push").
package git

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// DefaultBranch is every repository's default branch, which its HEAD names; nobody changes it.
const DefaultBranch = "main"

// ZeroSHA is the object name Git gives a ref that a push creates (its old value) or deletes (its new one).
const ZeroSHA = "0000000000000000000000000000000000000000"

// Ref is one of a repository's refs and the object it names.
type Ref struct {
	Name string
	SHA  string
}

// Snapshot is what a fetch serves from: a repository's refs and its active packs, read at one moment, so every ref's
// objects are in a pack it lists. Its packs carry their Name and Upload.
type Snapshot struct {
	Refs  []Ref
	Packs []Pack
}

// Pack is a pack of a repository's objects as the bucket holds it: git/<repo-id>/<Upload>/<Name>.pack and its .idx. A
// pack's name is its content's hash, so a push that brings the same objects again brings a pack of the same name; each
// upload of a pack's files has an ID of its own, so maintenance deletes only an upload no row lists.
type Pack struct {
	Name    string
	Upload  uuid.UUID
	Size    int64
	Objects int64
}

// RefUpdate is one ref a push moves, from Old to New; ZeroSHA as Old creates the ref, as New deletes it.
type RefUpdate struct {
	Name string
	Old  string
	New  string
}

// Person is a commit's author or committer as the commit writes them, with the zone their time was written in, such
// as +0200.
type Person struct {
	Name  string
	Email string
	At    time.Time
	Zone  string
}

// Commit is a commit a push brings, read whole from its object.
type Commit struct {
	SHA       string
	Tree      string
	Parents   []string
	Author    Person
	Committer Person
	Message   string
	// Signature is the commit's signature as written, kept and not yet checked; empty when unsigned.
	Signature string
}

// Transport is how a push reached the Git server.
type Transport string

// The transports.
const (
	TransportHTTPS Transport = "https"
	TransportSSH   Transport = "ssh"
	// TransportAPI is a merge a principal asked Cobranch for, which the Git server built.
	TransportAPI Transport = "api"
)

// Push is a push's manifest, which the Git server records once Git has checked its objects: who pushed,
// the refs it moves, its pack (nil when it only deletes), every commit it brings that the repository's refs didn't
// reach, and what its refs say of the branches once it applies.
type Push struct {
	// ID names the push: the Git server asks again with it when it lost the answer.
	ID     uuid.UUID
	RepoID uuid.UUID
	// OrgID is the repository's organization, whose Git data limit holds the push's pack.
	OrgID      uuid.UUID
	PusherID   uuid.UUID
	Transport  Transport
	Updates    []RefUpdate
	Pack       *Pack
	Commits    []Commit
	ReceivedAt time.Time
	// Requirements replace the repository's merge requirements when the push moves the default branch: those of
	// SettingsFile at its new tip, none without one or when the push deletes the branch.
	Requirements []Requirements
	// Facts are every live branch's but the default's once the push applies, when it moves a branch; nil when it moves
	// none.
	Facts []Facts
	// Tips are the branches Facts were counted from: every live branch once the push applies, by name, each with the
	// commit it names; nil when the push moves no branch. Another push recorded since may have moved one.
	Tips map[string]string
	// Brought is what each move of a branch to a commit brings into it; nil when the push moves no branch to one.
	Brought []Brought
	// Bypass is whether the pusher holds repo.bypass, which pushes past merge requirements.
	Bypass bool
	// Merging is the branch a merge asked of Cobranch (TransportAPI) merges, at the head it merges: it must be Ready
	// there, whatever its base requires, unless the pusher holds repo.bypass. nil for a client's push.
	Merging *Head
	// GitDataLimit is the most bytes of active packs the organization may hold once the push's pack is listed, which
	// the store holds the push to in its transaction; 0 for none. Service.Record sets it.
	GitDataLimit int64
}

// Merges are the branches whose heads the push brings into the default branch when it moves it on from a commit: the
// merges the push worker records. Creating the default branch merges nothing.
func (p Push) Merges() []Head {
	for _, u := range p.Updates {
		if u.Name != "refs/heads/"+DefaultBranch || u.Old == ZeroSHA {
			continue
		}
		for _, b := range p.Brought {
			if b.Branch == DefaultBranch {
				return b.Heads
			}
		}
	}
	return nil
}

// MovesDefault reports whether the push moves the default branch: creates, moves or deletes it.
func (p Push) MovesDefault() bool {
	for _, u := range p.Updates {
		if u.Name == "refs/heads/"+DefaultBranch {
			return true
		}
	}
	return false
}

// SettingsFile is the path of a repository's settings in its default branch, whose branches key names its branches'
// merge requirements (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/branch-status.md,
// "Merge requirements").
const SettingsFile = "cobranch.yaml"

// Requirements are what a merge into a branch needs, as SettingsFile names them under branches: the annotations the
// incoming head must carry, Successful, whether history stays linear, and the reviews of the incoming head it needs,
// nil when it requires none. A branch named there has requirements even when they list nothing.
type Requirements struct {
	Branch        string
	Annotations   []string
	LinearHistory bool
	Reviews       *ReviewRequirements
}

// ReviewRequirements are the reviews a merge into a branch needs of the incoming head: no reviewer's latest review of
// it requesting changes, and at least Approvals people's latest reviews approving it, besides its author's and its
// pusher's.
type ReviewRequirements struct {
	Approvals int
}

// MaxApprovals is the most approvals a branch's requirements name.
const MaxApprovals = 10

// Facts are what Git's objects say of a live branch against the default branch: how many commits it has that the
// default branch lacks (Ahead), how many the default branch has that it lacks (Behind), and how many of those it has
// are merges (Merges), counted only while the default branch requires linear history; nil otherwise. Without a
// default branch, Ahead counts all of its commits.
type Facts struct {
	Branch string
	Ahead  int
	Behind int
	Merges *int
}

// Head is a branch at a commit.
type Head struct {
	Branch string
	SHA    string
}

// Brought is what a push's move of a branch to a commit brings into it: the other live branches, besides the default,
// whose heads it brings in (Heads), and how many of the commits it adds, merges aside, none of them nor the default
// branch holds (Orphans). A move adds the commits its new tip has and its old one lacked; creating a branch adds those
// the default branch lacks, or, for the default branch itself, all its commits
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/branch-status.md, "By a push").
type Brought struct {
	Branch  string
	Heads   []Head
	Orphans int
	// Merges counts the merge commits it adds that the default branch doesn't hold, and Unclean lists those of them none
	// of those branches holds whose tree isn't what Git merges their parents into: a conflict resolved by hand, changes
	// of their own, or more than two parents. Only a move of a branch with merge requirements counts them.
	Merges  int
	Unclean []string
}

// RefusedError is a push that a branch's merge requirements refuse, with what the client sees.
type RefusedError struct {
	Message string
}

func (e *RefusedError) Error() string { return e.Message }

// Answer is what the client learns of its push: accepted, or refused with the reason, which it shows as remote: lines;
// Stale when another push moved a ref first.
type Answer struct {
	Accepted bool
	Message  string
	Stale    bool
}

// The messages a client sees, as the design's table of accepted pushes words them.
const (
	// MessageNotRecorded is a push that the bucket or the database failed.
	MessageNotRecorded = "Cobranch could not record the push; nothing changed, so try again"
	// messageReserved is a ref under cobranch/.
	messageReserved = "Cobranch writes refs under cobranch/ itself"
)

// StaleError is a push whose old value of Ref is no longer the ref's: another push moved it first. With During, Ref
// is a branch the push doesn't move, which another push moved while this one was on its way, so the facts it counted
// of the branches no longer hold.
type StaleError struct {
	Ref    string
	During bool
}

func (e *StaleError) Error() string {
	if e.During {
		return fmt.Sprintf("%s moved while Cobranch took your push; nothing changed, so push again", e.Ref)
	}
	return fmt.Sprintf("%s moved since you fetched: fetch, merge or rebase, then push again", e.Ref)
}

// ErrNotFound is a repository that isn't there.
var ErrNotFound = errors.New("not found")

// Refusal returns why Cobranch refuses updates, or "" when it accepts them: only branches and tags, none under
// cobranch/, which only Cobranch writes, and each ref once. One refused ref refuses the whole push.
func Refusal(updates []RefUpdate) string {
	seen := map[string]bool{}
	for _, u := range updates {
		switch {
		case strings.HasPrefix(u.Name, "refs/heads/cobranch/") || strings.HasPrefix(u.Name, "refs/tags/cobranch/"):
			return messageReserved
		case !strings.HasPrefix(u.Name, "refs/heads/") && !strings.HasPrefix(u.Name, "refs/tags/"):
			return "Cobranch keeps branches and tags only: " + u.Name + " refused"
		case seen[u.Name]:
			return u.Name + " is updated twice"
		case !validSHA(u.Old) || !validSHA(u.New) || u.Old == u.New:
			return u.Name + ": not an update Git sends"
		}
		seen[u.Name] = true
	}
	return ""
}

// validSHA reports whether sha is a SHA-1 object name, in lowercase hex.
func validSHA(sha string) bool {
	if len(sha) != len(ZeroSHA) {
		return false
	}
	for _, c := range sha {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Branch names the branch ref is, and whether it is one.
func Branch(ref string) (string, bool) {
	return strings.CutPrefix(ref, "refs/heads/")
}
