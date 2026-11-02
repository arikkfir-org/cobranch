// Package chats holds the chats on branches: every branch carries a chat of threads of Markdown messages, a thread
// perhaps anchored to lines of a file at a commit, which its viewers follow live
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/agents.md, "Branch chats").
package chats

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/orgs"
)

// The permissions chats take, on the repository.
const (
	// PermPost starts threads and posts messages, and resolves, opens again, edits and deletes the viewer's own.
	PermPost = "messages.post"
	// PermModerate resolves and opens again any thread, and deletes any message.
	PermModerate = "messages.moderate"
)

// Viewer is who an operation runs for: a signed-in user, or the user or API key a credential acts as.
type Viewer = auth.User

// Principal is who started a thread, posted a message, resolved one or deleted the other.
type Principal = code.Principal

// Subject is what a chat belongs to: one of a repository's branches, by its row, so a branch deleted and pushed again
// is another, with a chat of its own. The service answers it to a viewer who reads the repository, and every operation
// on it checks that again.
type Subject struct {
	OrgID    uuid.UUID
	RepoID   uuid.UUID
	BranchID uuid.UUID
	// Branch is the branch's name, without refs/heads/.
	Branch string
	// Head is the object the branch is at.
	Head string
	// Org and Repo are the slugs of the repository's organization and its own, where messages' relative links lead.
	Org  string
	Repo string
}

// Anchor points a thread at lines StartLine to EndLine, from 1, of the file at Path of commit SHA. It stays there when
// the branch moves on.
type Anchor struct {
	SHA       string
	Path      string
	StartLine int
	EndLine   int
}

// Thread is one of a chat's threads, with its messages in the order they were posted.
type Thread struct {
	ID     uuid.UUID
	Anchor *Anchor
	// Outdated is whether the branch has moved on from the commit its anchor names; false without an anchor.
	Outdated bool
	// StartedBy is who started it; nil once they are gone.
	StartedBy *Principal
	CreatedAt time.Time
	// ResolvedAt is when it was resolved; nil while it is open.
	ResolvedAt *time.Time
	// ResolvedBy is who resolved it; nil while it is open, and once they are gone.
	ResolvedBy *Principal
	// Version counts its changes from 1: it is its ETag, which resolving or opening it again names.
	Version int64
	// Messages are its messages; an update's thread comes without them.
	Messages []Message
}

// Message is one of a thread's messages, in Markdown.
type Message struct {
	ID       uuid.UUID
	ThreadID uuid.UUID
	// Body is its Markdown; empty once it is deleted.
	Body string
	// HTML is its body rendered as the code browsing design renders a README, its relative links leading to the
	// branch's files; empty once it is deleted.
	HTML string
	// Author is who posted it; nil once they are gone.
	Author    *Principal
	CreatedAt time.Time
	// EditedAt is when its author last edited it; nil until they do.
	EditedAt *time.Time
	// DeletedAt is when it was deleted; nil while it is there.
	DeletedAt *time.Time
	// DeletedBy is who deleted it; nil while it is there, and once they are gone.
	DeletedBy *Principal
	// Version counts its changes from 1: it is its ETag, which an edit or a deletion names.
	Version int64
}

// Page is a page of a chat's threads, newest first, each with its messages.
type Page struct {
	Threads []Thread
	// Next is where the next page starts, the last thread's ID; uuid.Nil after the last page.
	Next uuid.UUID
	// After is where the chat's updates stood when the page was read: updates after it hold every change the page may
	// lack.
	After int64
}

// UpdateKind is what changed in a chat.
type UpdateKind string

// The changes to a chat. Each is event chat.<kind>, whose subject is the chat's branch.
const (
	ThreadStarted  UpdateKind = "thread.started"
	MessagePosted  UpdateKind = "message.posted"
	MessageEdited  UpdateKind = "message.edited"
	MessageDeleted UpdateKind = "message.deleted"
	ThreadResolved UpdateKind = "thread.resolved"
	ThreadReopened UpdateKind = "thread.reopened"
)

// Update is a change to a chat, with the thread and the message it changed as they are now, which may be later than
// the change: a viewer who applies updates in order, replacing what they hold by ID, holds the chat as it is.
type Update struct {
	// Cursor is where the update stands among the chat's updates: the next read starts after it.
	Cursor int64
	Kind   UpdateKind
	// Thread is the thread it changed, or the thread of the message it changed, without its messages.
	Thread Thread
	// Message is the message it posted, edited or deleted, or a started thread's first; nil when it resolved a thread
	// or opened one again.
	Message *Message
}

// ThreadInput starts a thread with its first message.
type ThreadInput struct {
	// ID is the thread's ID, a UUIDv7 its starter picks so that starting it again finds it; uuid.Nil has Cobranch pick
	// one.
	ID uuid.UUID
	// Anchor points the thread at lines of a file at a commit the repository holds; nil for none.
	Anchor *Anchor
	// Body is its first message's Markdown.
	Body string
}

// MessageInput posts a message to a thread.
type MessageInput struct {
	// ID is the message's ID, a UUIDv7 its poster picks so that posting it again finds it; uuid.Nil has Cobranch pick
	// one.
	ID   uuid.UUID
	Body string
}

// The versions an edit, a deletion or a resolution names that are no message's or thread's own.
const (
	// AnyVersion changes whichever version is there (If-Match: *).
	AnyVersion int64 = -1
)

// MaxBody is the longest message, in characters.
const MaxBody = 20000

// maxPath is the longest path an anchor names, in bytes.
const maxPath = 4096

// MaxPage is the most threads a page holds.
const MaxPage = 100

// ErrChanged is an edit, a deletion or a resolution that names a version of a message or a thread it no longer is.
var ErrChanged = errors.New("it changed since it was read")

// ErrIDTaken is a thread started or a message posted with an ID that names another thread or message: another
// poster's, or one of another thread or chat.
var ErrIDTaken = errors.New("another thread or message has this ID")

// ErrBranchDeleted is a change to the chat of a branch that was deleted, which stays as it was.
var ErrBranchDeleted = errors.New("the branch was deleted; its chat stays as it was")

// ErrDeleted is an edit of a message that was deleted.
var ErrDeleted = errors.New("the message was deleted")

// hexName is a commit's full name: 40 lowercase hexadecimal digits in a SHA-1 repository, 64 in a SHA-256 one.
var hexName = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// ValidateBody reports whether body is a message: not blank, and at most MaxBody characters of UTF-8.
func ValidateBody(body string) error {
	switch {
	case strings.TrimSpace(body) == "":
		return &orgs.ValidationError{Field: "body", Code: "body.empty", Message: "a message says something"}
	case !utf8.ValidString(body):
		return &orgs.ValidationError{Field: "body", Code: "body.invalid", Message: "a message is UTF-8 text"}
	case utf8.RuneCountInString(body) > MaxBody:
		return &orgs.ValidationError{Field: "body", Code: "body.too_long", Message: fmt.Sprintf("a message is at most %d characters", MaxBody)}
	}
	return nil
}

// ValidateAnchor reports whether a points at lines of a file at a commit: the commit's full name, a path relative to
// the root as names separated by /, without . or .. and at most 4096 bytes, and lines from 1, the last at or after
// the first.
func ValidateAnchor(a Anchor) error {
	invalid := func(field, message string) error {
		return &orgs.ValidationError{Field: "anchor." + field, Code: "anchor.invalid", Message: message}
	}
	switch {
	case !hexName.MatchString(a.SHA):
		return invalid("sha", "an anchor names its commit by its full name, in lowercase hexadecimal")
	case a.Path == "", len(a.Path) > maxPath, path.Clean(a.Path) != a.Path, strings.HasPrefix(a.Path, "/"), a.Path == ".",
		a.Path == "..", strings.HasPrefix(a.Path, "../"), !utf8.ValidString(a.Path):
		return invalid("path", "an anchor names a file by its path from the root, as names separated by /")
	case a.StartLine < 1:
		return invalid("startLine", "an anchor's lines start at 1")
	case a.EndLine < a.StartLine:
		return invalid("endLine", "an anchor's last line is at or after its first")
	}
	return nil
}

// validateID reports whether id, when it is set, is a UUIDv7, which orders threads and messages by when they came.
func validateID(id uuid.UUID) error {
	if id != uuid.Nil && id.Version() != 7 {
		return &orgs.ValidationError{Field: "id", Code: "id.invalid", Message: "an ID a poster picks is a UUIDv7"}
	}
	return nil
}
