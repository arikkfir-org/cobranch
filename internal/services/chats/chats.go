// Package chats holds the chats on branches and issues: every branch carries a chat of threads of Markdown messages, a
// thread perhaps anchored to lines of a file at a commit, which its viewers follow live
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/agents.md, "Branch chats"), and an
// issue's comments are a chat of its own, whose threads point at no file
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/issues.md, "Comments"). Beside them, each
// person has a private conversation, a chat only they read, about their organization, each of its repositories and
// each of its agents (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/conversations.md).
package chats

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/orgs"
)

// The permissions a branch's chat takes, on the repository.
const (
	// PermPost starts threads and posts messages, and resolves, opens again, edits and deletes the viewer's own.
	PermPost = code.PermPostMessages
	// PermModerate resolves and opens again any thread, and deletes any message.
	PermModerate = "messages.moderate"
)

// The permissions an issue's chat takes, at the organization: the issues' own, issues.PermRead, issues.PermWrite and
// issues.PermManage, named here so that the issues service may use this one. Reading the issue reads its chat, writing
// it posts there as messages.post does, and managing it moderates there.
const (
	permIssueRead     = "issues.read"
	permIssuePost     = "issues.write"
	permIssueModerate = "issues.manage"
)

// Viewer is who an operation runs for: a signed-in user, or the user or API key a credential acts as.
type Viewer = auth.User

// Principal is who started a thread, posted a message, resolved one or deleted the other.
type Principal = code.Principal

// Kind is what a chat belongs to.
type Kind string

// The kinds of chats: a branch's and an issue's, which their subjects' readers share, and a person's private
// conversations about an organization, one of its repositories or one of its agents.
const (
	KindBranch       Kind = "branch"
	KindIssue        Kind = "issue"
	KindOrganization Kind = "organization"
	KindRepository   Kind = "repository"
	KindAgent        Kind = "agent"
)

// Subject is what a chat belongs to: one of a repository's branches, by its row, so a branch deleted and pushed again
// is another, with a chat of its own; one of an organization's issues, whose comments it holds; or a person's private
// conversation about the organization, one of its repositories or one of its agents. The service answers it to a
// viewer who reads the repository or the issue, or whose conversation it is and who reads what it is about, and every
// operation on it checks that again.
type Subject struct {
	OrgID uuid.UUID
	// RepoID is the branch's repository, or the repository a conversation is about; uuid.Nil otherwise.
	RepoID uuid.UUID
	// BranchID is the branch's; uuid.Nil for any other chat.
	BranchID uuid.UUID
	// Branch is the branch's name, without refs/heads/, or for a repository's conversation its default branch, where
	// its messages' relative links lead.
	Branch string
	// Head is the object that branch is at; empty for a repository without it.
	Head string
	// IssueID is the issue's; uuid.Nil for any other chat.
	IssueID uuid.UUID
	// Number is the issue's number in its organization, and Issue its identifier, such as ACME-42.
	Number int
	Issue  string
	// ConversationID is a private conversation's (ConversationID); uuid.Nil for a branch's chat or an issue's.
	ConversationID uuid.UUID
	// OwnerID is the principal whose private conversation it is, who alone reads it; uuid.Nil for a shared chat.
	OwnerID uuid.UUID
	// AgentID and Agent name the agent a conversation is with, by its principal and its name; uuid.Nil and empty
	// otherwise.
	AgentID uuid.UUID
	Agent   string
	// Org and Repo are the slugs of the subject's organization and of the branch's repository, or the repository's a
	// conversation is about, where messages' relative links lead.
	Org  string
	Repo string
}

// ID is the subject's own ID, its branch's, its issue's or its conversation's, which its chat's events name.
func (s Subject) ID() uuid.UUID {
	switch {
	case s.ConversationID != uuid.Nil:
		return s.ConversationID
	case s.IssueID != uuid.Nil:
		return s.IssueID
	}
	return s.BranchID
}

// Kind is what the chat belongs to.
func (s Subject) Kind() Kind {
	switch {
	case s.ConversationID != uuid.Nil && s.AgentID != uuid.Nil:
		return KindAgent
	case s.ConversationID != uuid.Nil && s.RepoID != uuid.Nil:
		return KindRepository
	case s.ConversationID != uuid.Nil:
		return KindOrganization
	case s.IssueID != uuid.Nil:
		return KindIssue
	}
	return KindBranch
}

// OfIssue reports whether the chat is an issue's.
func (s Subject) OfIssue() bool {
	return s.Kind() == KindIssue
}

// Private reports whether the chat is a person's private conversation, which only they read.
func (s Subject) Private() bool {
	return s.ConversationID != uuid.Nil
}

// conversations is the namespace of the conversations' IDs.
var conversations = uuid.MustParse("6b9a3f5e-2c4d-4e1a-9f0b-8d7c6e5a4b3c")

// ConversationID is the ID of owner's private conversation about the organization orgID, about its repository repoID,
// or with its agent agentID; repoID and agentID are uuid.Nil but for the conversation that names one. It comes from
// the owner and the subject alone, so a conversation's events and followers name it before its first thread makes it.
func ConversationID(owner, orgID, repoID, agentID uuid.UUID) uuid.UUID {
	name := make([]byte, 0, 4*len(owner))
	for _, id := range []uuid.UUID{owner, orgID, repoID, agentID} {
		name = append(name, id[:]...)
	}
	return uuid.NewSHA1(conversations, name)
}

// Anchor points a thread at lines StartLine to EndLine, from 1, of the file at Path of commit SHA. It stays there when
// the branch moves on. A review's comments are anchored threads too, so the code package holds it.
type Anchor = code.Anchor

// Thread is one of a chat's threads, with its messages in the order they were posted.
type Thread struct {
	ID     uuid.UUID
	Anchor *Anchor
	// Outdated is whether the branch has moved on from the commit its anchor names, or for a review's comment from the
	// commit the review reviewed, whose diff it may anchor at its base; false without an anchor.
	Outdated bool
	// ReviewID names the review the thread is a comment of, which opened it; nil for a thread started in the chat, and
	// once its review is gone.
	ReviewID *uuid.UUID
	// ReviewedSHA is the commit the review it is a comment of reviewed; "" for a thread that is none.
	ReviewedSHA string
	// StartedBy is who started it; nil once they are gone.
	StartedBy *Principal
	CreatedAt time.Time
	// ResolvedAt is when it was resolved; nil while it is open.
	ResolvedAt *time.Time
	// ResolvedBy is who resolved it; nil while it is open, and once they are gone.
	ResolvedBy *Principal
	// Version counts its changes and its messages' from 1: it is its ETag, which resolving or opening it again names.
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
	// Task is the task of the agent that posted it; nil for a person's message, and once the task is deleted.
	Task *TaskRef
	// Version counts its changes from 1: it is its ETag, which an edit or a deletion names.
	Version int64
}

// TaskRef names an agent's task: its ID, and its number in its organization.
type TaskRef struct {
	ID     uuid.UUID
	Number int64
}

// Poster is who starts a thread or posts a message: a principal, and, for an agent, the task it works, which its
// messages link; uuid.Nil for none.
type Poster struct {
	ID, Task uuid.UUID
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

// The changes to a chat. Each is event chat.<kind>, whose subject is the chat's branch or issue.
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
	// Anchor points the thread at lines of a file at a commit the repository holds; nil for none, as on an issue.
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
const MaxBody = code.MaxMessageBody

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

// ValidateBody reports whether body is a message: not blank, and at most MaxBody characters of UTF-8.
func ValidateBody(body string) error {
	return code.ValidateMessage("body", body) //nolint:wrapcheck // a validation error is the caller's answer as it is
}

// ValidateAnchor reports whether a points at lines of a file at a commit: the commit's full name, a path relative to
// the root as names separated by /, without . or .. and at most 4096 bytes, and lines from 1, the last at or after
// the first.
func ValidateAnchor(a Anchor) error {
	return code.ValidateAnchor("anchor", a) //nolint:wrapcheck // a validation error is the caller's answer as it is
}

// validateID reports whether id, when it is set, is a UUIDv7, which orders threads and messages by when they came.
func validateID(id uuid.UUID) error {
	if id != uuid.Nil && id.Version() != 7 {
		return &orgs.ValidationError{Field: "id", Code: "id.invalid", Message: "an ID a poster picks is a UUIDv7"}
	}
	return nil
}
