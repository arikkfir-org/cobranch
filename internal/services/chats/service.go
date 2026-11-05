package chats

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/orgs"
	"cobranch.dev/internal/services/repos"
)

var tracer = otel.Tracer("cobranch.dev/internal/services/chats")

// Store keeps the chats. Every change it makes appends the chat's event, whose subject is the chat's branch, issue or
// conversation, and wakes the servers that follow it once its transaction commits; a change to the chat of a deleted
// branch is ErrBranchDeleted, and to that of a deleted issue, or a conversation whose subject or owner is gone,
// repos.ErrNotFound. A change to a conversation makes it, the first time.
type Store interface {
	// Subject finds a repository's branch by name as a chat's subject: the live one, or else the one of that name deleted
	// last; repos.ErrNotFound when no branch had the name.
	Subject(ctx context.Context, repoID uuid.UUID, branch string) (Subject, error)
	// Issue finds one of the organization's issues by number as a chat's subject, or repos.ErrNotFound.
	Issue(ctx context.Context, orgID uuid.UUID, number int) (Subject, error)
	// Conversation finds owner's private conversation as a chat's subject: about the organization, about its repository
	// repoID, or with its agent agentID, the other uuid.Nil; repos.ErrNotFound when the organization has no such
	// repository or agent. A conversation without threads yet has its subject too, which its first thread makes.
	Conversation(ctx context.Context, owner, orgID, repoID, agentID uuid.UUID) (Subject, error)
	// Head finds the object a branch is at now, deleted or not, or repos.ErrNotFound.
	Head(ctx context.Context, branchID uuid.UUID) (string, error)
	// HasCommit reports whether the repository holds a commit.
	HasCommit(ctx context.Context, repoID uuid.UUID, sha string) (bool, error)
	// Threads lists at most limit of the subject's threads, newest first, from the one before before names, or from
	// the newest when it is uuid.Nil, each with its messages; and the cursor of the chat's updates, read first.
	Threads(ctx context.Context, subj Subject, before uuid.UUID, limit int) ([]Thread, int64, error)
	// Thread finds one of the subject's threads with its messages, or repos.ErrNotFound.
	Thread(ctx context.Context, subj Subject, id uuid.UUID) (Thread, error)
	// Message finds a message of one of the subject's threads, or repos.ErrNotFound.
	Message(ctx context.Context, subj Subject, id uuid.UUID) (Message, error)
	// StartThread starts the subject's thread id with its first message, messageID, for by, and answers it and true;
	// or, when a thread of id is there, answers it and false, and ErrIDTaken when it isn't the subject's. A thread an
	// agent's task starts in its branch's chat, or its issue's, becomes the one the task talks in, when it talks in
	// none yet.
	StartThread(ctx context.Context, by Poster, subj Subject, id, messageID uuid.UUID, anchor *Anchor, body string) (Thread, bool, error)
	// Post posts message id to one of the subject's threads for by, and answers it and true; or, when a message of id
	// is there, answers it and false, and ErrIDTaken when it isn't the thread's.
	Post(ctx context.Context, by Poster, subj Subject, threadID, id uuid.UUID, body string) (Message, bool, error)
	// Edit changes the body of a live message of the subject's, at version or at any version for AnyVersion, or
	// ErrChanged.
	Edit(ctx context.Context, actor uuid.UUID, subj Subject, id uuid.UUID, version int64, body string) (Message, error)
	// Delete deletes a live message of the subject's for actor, at version or at any version for AnyVersion, or
	// ErrChanged.
	Delete(ctx context.Context, actor uuid.UUID, subj Subject, id uuid.UUID, version int64) error
	// Resolve resolves one of the subject's threads for actor, or opens it again, at version or at any version for
	// AnyVersion, and answers it with its messages, or ErrChanged.
	Resolve(ctx context.Context, actor uuid.UUID, subj Subject, id uuid.UUID, version int64, resolved bool) (Thread, error)
	// Updates lists at most limit of the subject's chat updates after cursor after, in order, each with its thread and
	// message as they are now.
	Updates(ctx context.Context, subj Subject, after int64, limit int) ([]Update, error)
	// Transact runs fn in one transaction that acts for actor, which the store's changes and the agents' store's join
	// through fn's context: none of them commits unless fn succeeds.
	Transact(ctx context.Context, actor uuid.UUID, fn func(ctx context.Context) error) error
}

// Notifier tells the servers that follow a chat when it may have changed.
type Notifier interface {
	// Watch returns a channel that receives soon after a change to the chat of a subject, a branch, an issue or a
	// conversation, commits, and a function that stops it. Changes that come while the channel is full make one
	// receive.
	Watch(subjectID uuid.UUID) (<-chan struct{}, func())
}

// MaxUpdates is the most updates a read answers.
const MaxUpdates = 100

// poll is how often a follower reads its chat again without a wake, for a notification it missed.
const poll = time.Minute

// Service keeps the branches' chats for the viewers who read their repositories, the issues' for those who read the
// organization's issues, and each private conversation for its owner while they read what it is about: every
// operation checks repo.read, issues.read or org.read through the authorizer first, and answers repos.ErrNotFound
// without it, as for a repository or an issue that doesn't exist, and for another's conversation.
type Service struct {
	store    Store
	notifier Notifier
	markdown code.Renderer
	authz    *auth.Authorizer
	agents   Agents
	tasks    Tasks
}

// NewService returns a Service over store, rendering messages with markdown and checking permissions with authz;
// notifier wakes those who follow a chat. Without markdown, messages come as Markdown alone; without notifier, a
// follower reads its chat once a minute.
func NewService(store Store, notifier Notifier, markdown code.Renderer, authz *auth.Authorizer) *Service {
	return &Service{store: store, notifier: notifier, markdown: markdown, authz: authz}
}

// rules are the permissions a kind of chat takes, and where they are granted.
type rules struct {
	scope auth.Scope
	// view sees the chat's subject, which isn't there without it; read reads the chat, which is refused without it.
	view, read string
	// post and moderate are PermPost's and PermModerate's counterparts, and where says where they are held.
	post, moderate, where string
}

var (
	// branchRules are a branch's chat's: on its repository, which isn't there for a viewer who doesn't read it.
	branchRules = rules{scope: auth.ScopeRepository, view: repos.PermRead, read: repos.PermRead, post: PermPost, moderate: PermModerate,
		where: "on the repository"}
	// issueRules are an issue's chat's: at its organization, as the issues service takes them, which refuses a viewer
	// who sees the organization but not its issues.
	issueRules = rules{scope: auth.ScopeOrganization, view: orgs.PermRead, read: permIssueRead, post: permIssuePost, moderate: permIssueModerate,
		where: "at the organization"}
	// orgConversationRules are the rules of a conversation about the organization or with one of its agents, and
	// repoConversationRules of one about a repository: its owner, who alone reads it, posts and moderates there while
	// they read what it is about.
	orgConversationRules = rules{scope: auth.ScopeOrganization, view: orgs.PermRead, read: orgs.PermRead, post: orgs.PermRead, moderate: orgs.PermRead,
		where: "at the organization"}
	repoConversationRules = rules{scope: auth.ScopeRepository, view: repos.PermRead, read: repos.PermRead, post: repos.PermRead,
		moderate: repos.PermRead, where: "on the repository"}
)

// access is what a viewer may do in a chat they read, by its rules.
type access struct {
	post, moderate bool
	rules          rules
	// thread is the one thread the viewer reaches, a task's in another's conversation; uuid.Nil for every thread.
	thread uuid.UUID
}

// reaches answers repos.ErrNotFound for a thread the viewer doesn't reach, as for one that isn't there.
func (a access) reaches(threadID uuid.UUID) error {
	if a.thread != uuid.Nil && a.thread != threadID {
		return repos.ErrNotFound
	}
	return nil
}

// forbidden is the refusal of a change that takes permission, which the viewer lacks where the chat's rules hold it.
func (a access) forbidden(permission, what string) error {
	return fmt.Errorf("%w: %s %s%s", auth.ErrForbidden, permission, a.rules.where, what)
}

// holds returns what v may do in the subject's chat: repos.ErrNotFound when v doesn't see its subject, or it is
// another's conversation but for the thread of a task its owner asked v for there, and auth.ErrForbidden when v sees
// it but doesn't read its chat.
func (s *Service) holds(ctx context.Context, v Viewer, subj Subject) (access, error) {
	if subj.Private() && subj.OwnerID != v.ID {
		return s.holdsForTask(ctx, v, subj)
	}
	r, scopeID := rulesOf(subj)
	return s.holdsBy(ctx, v, r, scopeID)
}

// rulesOf is the rules of the subject's chat, and the ID of the scope where they hold.
func rulesOf(subj Subject) (rules, uuid.UUID) {
	switch subj.Kind() {
	case KindIssue:
		return issueRules, subj.OrgID
	case KindOrganization, KindAgent:
		return orgConversationRules, subj.OrgID
	case KindRepository:
		return repoConversationRules, subj.RepoID
	}
	return branchRules, subj.RepoID
}

// holdsBy returns what v may do in a chat of r's rules whose scope is scopeID, as holds does.
func (s *Service) holdsBy(ctx context.Context, v Viewer, r rules, scopeID uuid.UUID) (access, error) {
	held, err := s.authz.Holds(ctx, v.ID, r.scope, scopeID)
	if err != nil {
		return access{}, fmt.Errorf("reading the viewer's permissions: %w", err)
	}
	can := access{post: slices.Contains(held, r.post), moderate: slices.Contains(held, r.moderate), rules: r}
	switch {
	case !slices.Contains(held, r.view):
		return access{}, repos.ErrNotFound
	case !slices.Contains(held, r.read):
		return access{}, can.forbidden(r.read, "")
	}
	return can, nil
}

// poster checks that v posts in the subject's chat, and returns what v may do there.
func (s *Service) poster(ctx context.Context, v Viewer, subj Subject) (access, error) {
	can, err := s.holds(ctx, v, subj)
	if err != nil {
		return access{}, err
	}
	if !can.post {
		return access{}, can.forbidden(can.rules.post, "")
	}
	return can, nil
}

// on is the attributes of a span that acts on the subject's chat.
func on(subj Subject) []attribute.KeyValue {
	switch {
	case subj.Private():
		return []attribute.KeyValue{attribute.String("cobranch.organization.id", subj.OrgID.String()),
			attribute.String("cobranch.conversation.id", subj.ConversationID.String()), attribute.String("cobranch.conversation.kind", string(subj.Kind()))}
	case subj.OfIssue():
		return []attribute.KeyValue{attribute.String("cobranch.organization.id", subj.OrgID.String()), attribute.String("cobranch.issue.id", subj.IssueID.String())}
	}
	return []attribute.KeyValue{attribute.String("cobranch.repository.id", subj.RepoID.String()), attribute.String("cobranch.branch.id", subj.BranchID.String())}
}

// Branch finds the chat of one of the repository's branches by name, without refs/heads/: the live one, or else the
// one of that name deleted last, whose chat stays readable and refuses changes; it takes repo.read. A name no branch
// had is repos.ErrNotFound.
func (s *Service) Branch(ctx context.Context, v Viewer, repoID uuid.UUID, name string) (Subject, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Branch")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.branch", name))
	if _, err := s.holdsBy(ctx, v, branchRules, repoID); err != nil {
		return Subject{}, failed(span, "finding the chat", err)
	}
	subj, err := s.store.Subject(ctx, repoID, name)
	return subj, failed(span, "finding the chat", err)
}

// Issue finds the chat of one of the organization's issues by number, its comments; it takes issues.read. A number no
// issue has is repos.ErrNotFound, as is an organization the viewer may not see; one the viewer sees without reading
// its issues is auth.ErrForbidden.
func (s *Service) Issue(ctx context.Context, v Viewer, orgID uuid.UUID, number int) (Subject, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Issue")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()), attribute.Int("cobranch.issue.number", number))
	if _, err := s.holdsBy(ctx, v, issueRules, orgID); err != nil {
		return Subject{}, failed(span, "finding the chat", err)
	}
	subj, err := s.store.Issue(ctx, orgID, number)
	return subj, failed(span, "finding the chat", err)
}

// Organization finds the viewer's own conversation about the organization, which only they read; it takes org.read.
// An organization the viewer may not see is repos.ErrNotFound.
func (s *Service) Organization(ctx context.Context, v Viewer, orgID uuid.UUID) (Subject, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Organization")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()))
	return s.conversation(ctx, span, v, orgConversationRules, orgID, orgID, uuid.Nil, uuid.Nil)
}

// Repository finds the viewer's own conversation about one of the organization's repositories, which only they read;
// it takes repo.read. A repository the viewer doesn't read is repos.ErrNotFound.
func (s *Service) Repository(ctx context.Context, v Viewer, orgID, repoID uuid.UUID) (Subject, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Repository")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()), attribute.String("cobranch.repository.id", repoID.String()))
	return s.conversation(ctx, span, v, repoConversationRules, repoID, orgID, repoID, uuid.Nil)
}

// Agent finds the viewer's own conversation with one of the organization's agents, which only they read; it takes
// org.read. An agent the organization doesn't have is repos.ErrNotFound.
func (s *Service) Agent(ctx context.Context, v Viewer, orgID, agentID uuid.UUID) (Subject, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Agent")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.organization.id", orgID.String()), attribute.String("cobranch.agent.id", agentID.String()))
	return s.conversation(ctx, span, v, orgConversationRules, orgID, orgID, uuid.Nil, agentID)
}

// conversation checks that v reads the conversation's subject by r's rules at scopeID, then finds v's conversation
// about the organization, its repository repoID or its agent agentID; under the token of a task asked in a private
// conversation, the conversation of the task's assigner, where the task talks.
func (s *Service) conversation(ctx context.Context, span trace.Span, v Viewer, r rules, scopeID, orgID, repoID, agentID uuid.UUID) (Subject, error) {
	if _, err := s.holdsBy(ctx, v, r, scopeID); err != nil {
		return Subject{}, failed(span, "finding the conversation", err)
	}
	subj, err := s.store.Conversation(ctx, auth.PrivateFor(ctx, v.ID), orgID, repoID, agentID)
	return subj, failed(span, "finding the conversation", err)
}

// Threads lists a page of at most limit of the chat's threads, 1 to MaxPage, newest first, from the one before before
// names or from the newest when it is uuid.Nil, each with its messages; it takes repo.read, or issues.read for an
// issue's.
func (s *Service) Threads(ctx context.Context, v Viewer, subj Subject, before uuid.UUID, limit int) (Page, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Threads", trace.WithAttributes(on(subj)...))
	defer span.End()
	if limit < 1 || limit > MaxPage {
		return Page{}, &orgs.ValidationError{Field: "limit", Code: "limit.invalid", Message: fmt.Sprintf("a page holds 1 to %d threads", MaxPage)}
	}
	can, err := s.holds(ctx, v, subj)
	if err != nil {
		return Page{}, failed(span, "listing the threads", err)
	}
	if can.thread != uuid.Nil {
		return s.threadPage(ctx, span, subj, can.thread)
	}
	threads, after, err := s.store.Threads(ctx, subj, before, limit+1)
	if err != nil {
		return Page{}, failed(span, "listing the threads", err)
	}
	page := Page{Threads: threads, After: after}
	if len(threads) > limit {
		page.Threads = threads[:limit]
		page.Next = threads[limit-1].ID
	}
	for i := range page.Threads {
		if err := s.present(ctx, subj, &page.Threads[i]); err != nil {
			return Page{}, failed(span, "listing the threads", err)
		}
	}
	return page, nil
}

// threadPage is the page of the one thread of the subject's chat that a task reaches in another's conversation, with
// where the chat's updates stand; empty while the thread isn't the subject's.
func (s *Service) threadPage(ctx context.Context, span trace.Span, subj Subject, id uuid.UUID) (Page, error) {
	_, after, err := s.store.Threads(ctx, subj, uuid.Nil, 1)
	if err != nil {
		return Page{}, failed(span, "listing the threads", err)
	}
	page := Page{After: after}
	t, err := s.store.Thread(ctx, subj, id)
	switch {
	case errors.Is(err, repos.ErrNotFound):
		return page, nil
	case err != nil:
		return Page{}, failed(span, "listing the threads", err)
	}
	if err := s.present(ctx, subj, &t); err != nil {
		return Page{}, failed(span, "listing the threads", err)
	}
	page.Threads = []Thread{t}
	return page, nil
}

// Thread finds one of the chat's threads with its messages; it takes repo.read, or issues.read for an issue's. One that
// isn't there is repos.ErrNotFound.
func (s *Service) Thread(ctx context.Context, v Viewer, subj Subject, id uuid.UUID) (Thread, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Thread", trace.WithAttributes(on(subj)...))
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.chat.thread.id", id.String()))
	if err := s.reach(ctx, v, subj, id); err != nil {
		return Thread{}, failed(span, "reading the thread", err)
	}
	t, err := s.store.Thread(ctx, subj, id)
	if err == nil {
		err = s.present(ctx, subj, &t)
	}
	return t, failed(span, "reading the thread", err)
}

// Message finds a message of one of the chat's threads; it takes repo.read, or issues.read for an issue's. One that
// isn't there is repos.ErrNotFound.
func (s *Service) Message(ctx context.Context, v Viewer, subj Subject, threadID, id uuid.UUID) (Message, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Message", trace.WithAttributes(on(subj)...))
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.chat.message.id", id.String()))
	if err := s.reach(ctx, v, subj, threadID); err != nil {
		return Message{}, failed(span, "reading the message", err)
	}
	m, err := s.message(ctx, subj, threadID, id)
	if err == nil {
		err = s.render(ctx, subj, &m)
	}
	return m, failed(span, "reading the message", err)
}

// StartThread starts a thread on the chat for the viewer with its first message, perhaps anchored to lines of a file at
// one of the repository's commits, perhaps asking one of the organization's agents, whose task then talks in the
// thread; it takes messages.post, or issues.write for an issue's, whose threads have no anchor, and agents.assign to
// ask. In an agent's conversation, a thread asks that agent unless it asks another. Starting the viewer's thread again
// by its ID answers it as it is; an ID of another thread is ErrIDTaken.
func (s *Service) StartThread(ctx context.Context, v Viewer, subj Subject, in ThreadInput) (Thread, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.StartThread", trace.WithAttributes(on(subj)...))
	defer span.End()
	can, err := s.poster(ctx, v, subj)
	if err != nil {
		return Thread{}, failed(span, "starting the thread", err)
	}
	if can.thread != uuid.Nil {
		return Thread{}, failed(span, "starting the thread", fmt.Errorf("%w: a task talks in its own thread of another's conversation alone", auth.ErrForbidden))
	}
	if err := s.validateThread(ctx, subj, in); err != nil {
		return Thread{}, failed(span, "starting the thread", err)
	}
	if in.Ask == uuid.Nil && subj.Kind() == KindAgent && s.agents != nil {
		in.Ask = subj.AgentID
	}
	task, err := s.asking(ctx, v, subj, in.Ask, in.Body)
	if err != nil {
		return Thread{}, failed(span, "starting the thread", err)
	}
	id, err := orNew(in.ID)
	if err != nil {
		return Thread{}, failed(span, "starting the thread", err)
	}
	messageID, err := uuid.NewV7()
	if err != nil {
		return Thread{}, failed(span, "starting the thread", fmt.Errorf("naming the message: %w", err))
	}
	span.SetAttributes(attribute.String("cobranch.chat.thread.id", id.String()))
	var t Thread
	err = s.record(ctx, v, task, func(ctx context.Context) (uuid.UUID, bool, error) {
		var created bool
		var err error
		t, created, err = s.store.StartThread(ctx, asker(ctx, v, task), subj, id, messageID, in.Anchor, in.Body)
		if err == nil && !created && (t.StartedBy == nil || t.StartedBy.ID != v.ID) {
			err = ErrIDTaken
		}
		return id, created, err
	})
	if err == nil && task != nil {
		t, err = s.store.Thread(ctx, subj, id)
	}
	if err == nil {
		err = s.present(ctx, subj, &t)
	}
	return t, failed(span, "starting the thread", err)
}

// validateThread reports whether in starts a thread: a UUIDv7 or no ID, a message, and an anchor, if any, at lines
// of a file at a commit the repository holds, which an issue's chat has none of.
func (s *Service) validateThread(ctx context.Context, subj Subject, in ThreadInput) error {
	if err := validateID(in.ID); err != nil {
		return err
	}
	if err := ValidateBody(in.Body); err != nil {
		return err
	}
	if in.Anchor == nil {
		return nil
	}
	if subj.OfIssue() {
		return &orgs.ValidationError{Field: "anchor", Code: "anchor.not_allowed", Message: "an issue's threads point at no file"}
	}
	if subj.Private() {
		return &orgs.ValidationError{Field: "anchor", Code: "anchor.not_allowed", Message: "a conversation's threads point at no file"}
	}
	if err := ValidateAnchor(*in.Anchor); err != nil {
		return err
	}
	held, err := s.store.HasCommit(ctx, subj.RepoID, in.Anchor.SHA)
	if err != nil {
		return fmt.Errorf("finding the anchor's commit: %w", err)
	}
	if !held {
		return &orgs.ValidationError{Field: "anchor.sha", Code: "anchor.commit_not_found", Message: "the repository holds no such commit"}
	}
	return nil
}

// Post posts a message to one of the chat's threads for the viewer, perhaps asking one of the organization's agents,
// whose task then talks in the thread, once no other task talks there; it takes messages.post, or issues.write for an
// issue's, and agents.assign to ask. Posting the viewer's message to the thread again by its ID answers it as it is;
// an ID of another message is ErrIDTaken.
func (s *Service) Post(ctx context.Context, v Viewer, subj Subject, threadID uuid.UUID, in MessageInput) (Message, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Post", trace.WithAttributes(on(subj)...))
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.chat.thread.id", threadID.String()))
	can, err := s.poster(ctx, v, subj)
	if err == nil {
		err = can.reaches(threadID)
	}
	if err != nil {
		return Message{}, failed(span, "posting the message", err)
	}
	if err := validateID(in.ID); err != nil {
		return Message{}, failed(span, "posting the message", err)
	}
	if err := ValidateBody(in.Body); err != nil {
		return Message{}, failed(span, "posting the message", err)
	}
	task, err := s.asking(ctx, v, subj, in.Ask, in.Body)
	if err != nil {
		return Message{}, failed(span, "posting the message", err)
	}
	id, err := orNew(in.ID)
	if err != nil {
		return Message{}, failed(span, "posting the message", err)
	}
	var m Message
	err = s.record(ctx, v, task, func(ctx context.Context) (uuid.UUID, bool, error) {
		var created bool
		var err error
		m, created, err = s.store.Post(ctx, asker(ctx, v, task), subj, threadID, id, in.Body)
		if err == nil && !created && (m.Author == nil || m.Author.ID != v.ID) {
			err = ErrIDTaken
		}
		return threadID, created, err
	})
	if err == nil && task != nil {
		m, err = s.store.Message(ctx, subj, id)
	}
	if err == nil {
		err = s.render(ctx, subj, &m)
	}
	return m, failed(span, "posting the message", err)
}

// Edit changes the body of one of the viewer's messages in one of the chat's threads, at version or at any version
// for AnyVersion; it takes messages.post, or issues.write for an issue's, and only a message's author edits it. A message at another version is
// ErrChanged, and a deleted one ErrDeleted. An edit that changes nothing answers the message as it is.
func (s *Service) Edit(ctx context.Context, v Viewer, subj Subject, threadID, id uuid.UUID, body string, version int64) (Message, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Edit", trace.WithAttributes(on(subj)...))
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.chat.message.id", id.String()))
	can, err := s.poster(ctx, v, subj)
	if err == nil {
		err = can.reaches(threadID)
	}
	if err != nil {
		return Message{}, failed(span, "editing the message", err)
	}
	if err := ValidateBody(body); err != nil {
		return Message{}, failed(span, "editing the message", err)
	}
	m, err := s.message(ctx, subj, threadID, id)
	switch {
	case err != nil:
	case m.Author == nil || m.Author.ID != v.ID:
		err = fmt.Errorf("%w: only its author edits a message", auth.ErrForbidden)
	case m.DeletedAt != nil:
		err = ErrDeleted
	case stale(version, m.Version):
		err = ErrChanged
	case m.Body != body:
		m, err = s.store.Edit(ctx, v.ID, subj, id, version, body)
	}
	if err == nil {
		err = s.render(ctx, subj, &m)
	}
	return m, failed(span, "editing the message", err)
}

// Delete deletes a message of one of the chat's threads, at version or at any version for AnyVersion, which empties it
// and keeps its place, marked with who deleted it; it takes messages.post for the viewer's own and messages.moderate
// for another's, or issues.write and issues.manage for an issue's. A message at another version is ErrChanged; deleting a deleted one again changes nothing.
func (s *Service) Delete(ctx context.Context, v Viewer, subj Subject, threadID, id uuid.UUID, version int64) error {
	ctx, span := tracer.Start(ctx, "chats.Service.Delete", trace.WithAttributes(on(subj)...))
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.chat.message.id", id.String()))
	can, err := s.holds(ctx, v, subj)
	if err == nil {
		err = can.reaches(threadID)
	}
	if err != nil {
		return failed(span, "deleting the message", err)
	}
	m, err := s.message(ctx, subj, threadID, id)
	if err != nil {
		return failed(span, "deleting the message", err)
	}
	own := m.Author != nil && m.Author.ID == v.ID
	switch {
	case own && !can.post && !can.moderate:
		err = can.forbidden(can.rules.post, "")
	case !own && !can.moderate:
		err = can.forbidden(can.rules.moderate, ", for another's message")
	case m.DeletedAt != nil:
	case stale(version, m.Version):
		err = ErrChanged
	default:
		err = s.store.Delete(ctx, v.ID, subj, id, version)
	}
	return failed(span, "deleting the message", err)
}

// Resolve resolves one of the chat's threads, or opens it again when resolved is false, at version or at any version
// for AnyVersion, and answers it with its messages; it takes messages.post for a thread the viewer started and
// messages.moderate for another's, or issues.write and issues.manage for an issue's. A thread at another version is ErrChanged; one already so is answered as it is.
func (s *Service) Resolve(ctx context.Context, v Viewer, subj Subject, id uuid.UUID, resolved bool, version int64) (Thread, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Resolve", trace.WithAttributes(on(subj)...))
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.chat.thread.id", id.String()), attribute.Bool("cobranch.chat.resolved", resolved))
	can, err := s.holds(ctx, v, subj)
	if err == nil {
		err = can.reaches(id)
	}
	if err != nil {
		return Thread{}, failed(span, "resolving the thread", err)
	}
	t, err := s.store.Thread(ctx, subj, id)
	if err != nil {
		return Thread{}, failed(span, "resolving the thread", err)
	}
	own := t.StartedBy != nil && t.StartedBy.ID == v.ID
	switch {
	case own && !can.post && !can.moderate:
		err = can.forbidden(can.rules.post, "")
	case !own && !can.moderate:
		err = can.forbidden(can.rules.moderate, ", for another's thread")
	case (t.ResolvedAt != nil) == resolved:
	case stale(version, t.Version):
		err = ErrChanged
	default:
		t, err = s.store.Resolve(ctx, v.ID, subj, id, version, resolved)
	}
	if err == nil {
		err = s.present(ctx, subj, &t)
	}
	return t, failed(span, "resolving the thread", err)
}

// reach checks that v reads the subject's chat and reaches its thread threadID.
func (s *Service) reach(ctx context.Context, v Viewer, subj Subject, threadID uuid.UUID) error {
	can, err := s.holds(ctx, v, subj)
	if err != nil {
		return err
	}
	return can.reaches(threadID)
}

// message finds a message of the chat's thread, or repos.ErrNotFound.
func (s *Service) message(ctx context.Context, subj Subject, threadID, id uuid.UUID) (Message, error) {
	m, err := s.store.Message(ctx, subj, id)
	if err == nil && m.ThreadID != threadID {
		err = repos.ErrNotFound
	}
	return m, err
}

// Updates lists at most MaxUpdates of the chat's updates after cursor after, in order; it takes repo.read, or
// issues.read for an issue's, checked at every read. With none yet, it waits up to wait for one, reading again whenever the chat may have changed.
func (s *Service) Updates(ctx context.Context, v Viewer, subj Subject, after int64, wait time.Duration) ([]Update, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Updates", trace.WithAttributes(on(subj)...))
	defer span.End()
	span.SetAttributes(attribute.Int64("cobranch.chat.after", after))
	wake, stop := s.watch(subj)
	defer stop()
	timeout := time.NewTimer(max(wait, 0))
	defer timeout.Stop()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		list, err := s.read(ctx, v, subj, after)
		if err != nil || len(list) > 0 || wait <= 0 {
			return list, failed(span, "reading the updates", err)
		}
		select {
		case <-ctx.Done():
			return nil, failed(span, "reading the updates", ctx.Err())
		case <-timeout.C:
			return nil, nil
		case <-wake:
		case <-ticker.C:
		}
	}
}

// Follow sends the chat's updates after cursor after, in order, until ctx ends or the viewer no longer reads the
// repository, the issue or what their conversation is about, then closes the channel; it takes repo.read, or issues.read for an issue's, checked at
// every read. It reads whenever the chat may have
// changed, and once a minute for a change it missed.
func (s *Service) Follow(ctx context.Context, v Viewer, subj Subject, after int64) (<-chan Update, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Follow", trace.WithAttributes(on(subj)...))
	defer span.End()
	if _, err := s.holds(ctx, v, subj); err != nil {
		return nil, failed(span, "following the chat", err)
	}
	out := make(chan Update)
	go s.follow(ctx, v, subj, after, out)
	return out, nil
}

// follow sends the chat's updates after cursor after to out until ctx ends or a read fails, which it logs unless the
// viewer lost access, then closes out.
func (s *Service) follow(ctx context.Context, v Viewer, subj Subject, after int64, out chan<- Update) {
	defer close(out)
	wake, stop := s.watch(subj)
	defer stop()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		list, err := s.read(ctx, v, subj, after)
		if err != nil {
			if !errors.Is(err, repos.ErrNotFound) && !errors.Is(err, auth.ErrForbidden) && ctx.Err() == nil {
				slog.ErrorContext(ctx, "Following a chat failed", "subject", subj.ID(), "error", err)
			}
			return
		}
		for _, u := range list {
			select {
			case out <- u:
				after = u.Cursor
			case <-ctx.Done():
				return
			}
		}
		if len(list) == MaxUpdates {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-ticker.C:
		}
	}
}

// watch watches the subject's chat through the notifier: a channel that never receives without one.
func (s *Service) watch(subj Subject) (<-chan struct{}, func()) {
	if s.notifier == nil {
		return nil, func() {}
	}
	return s.notifier.Watch(subj.ID())
}

// read checks that v reads the chat, then lists at most MaxUpdates of the chat's updates after cursor after, at a
// branch's head now, by an issue's identifier now, or by a conversation's subject now: repos.ErrNotFound once the
// issue, or the repository or agent a conversation is about, is gone.
func (s *Service) read(ctx context.Context, v Viewer, subj Subject, after int64) ([]Update, error) {
	can, err := s.holds(ctx, v, subj)
	if err != nil {
		return nil, err
	}
	subj, err = s.now(ctx, subj)
	if err != nil {
		return nil, err
	}
	list, err := s.updates(ctx, subj, can, after)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if err := s.present(ctx, subj, &list[i].Thread); err != nil {
			return nil, err
		}
		if m := list[i].Message; m != nil {
			if err := s.render(ctx, subj, m); err != nil {
				return nil, err
			}
		}
	}
	return list, nil
}

// updates lists at most MaxUpdates of the chat's updates after cursor after, in order, of the thread can reaches
// alone when it reaches one: past as many updates of other threads as come before the first of it.
func (s *Service) updates(ctx context.Context, subj Subject, can access, after int64) ([]Update, error) {
	for {
		list, err := s.store.Updates(ctx, subj, after, MaxUpdates)
		if err != nil {
			return nil, fmt.Errorf("listing the chat's updates: %w", err)
		}
		if can.thread == uuid.Nil {
			return list, nil
		}
		kept := slices.DeleteFunc(slices.Clone(list), func(u Update) bool { return u.Thread.ID != can.thread })
		if len(kept) > 0 || len(list) < MaxUpdates {
			return kept, nil
		}
		after = list[len(list)-1].Cursor
	}
}

// now is the subject as it is now: a branch at its head now, an issue by its identifier now and a conversation by its
// subject's slugs and names now; repos.ErrNotFound once the issue, or what a conversation is about, is gone.
func (s *Service) now(ctx context.Context, subj Subject) (Subject, error) {
	switch subj.Kind() {
	case KindBranch:
		head, err := s.store.Head(ctx, subj.BranchID)
		if err != nil {
			return Subject{}, fmt.Errorf("reading the branch's head: %w", err)
		}
		subj.Head = head
		return subj, nil
	case KindIssue:
		now, err := s.store.Issue(ctx, subj.OrgID, subj.Number)
		if err == nil && now.IssueID != subj.IssueID {
			err = repos.ErrNotFound
		}
		if err != nil {
			return Subject{}, fmt.Errorf("reading the issue: %w", err)
		}
		return now, nil
	}
	now, err := s.store.Conversation(ctx, subj.OwnerID, subj.OrgID, repoOf(subj), subj.AgentID)
	if err != nil {
		return Subject{}, fmt.Errorf("reading the conversation's subject: %w", err)
	}
	return now, nil
}

// repoOf is the repository a repository's conversation is about; uuid.Nil for any other conversation.
func repoOf(subj Subject) uuid.UUID {
	if subj.Kind() == KindRepository {
		return subj.RepoID
	}
	return uuid.Nil
}

// present works out whether t is outdated, and renders its messages.
func (s *Service) present(ctx context.Context, subj Subject, t *Thread) error {
	if t.Anchor != nil {
		t.Outdated = cmp.Or(t.ReviewedSHA, t.Anchor.SHA) != subj.Head
	}
	for i := range t.Messages {
		if err := s.render(ctx, subj, &t.Messages[i]); err != nil {
			return err
		}
	}
	return nil
}

// render renders m's body as HTML, its relative links leading to the branch's files, or a repository's conversation's
// default branch's, to the organization's pages beside an issue's, as its description's do, or to the organization's
// pages from an organization's or an agent's conversation.
func (s *Service) render(ctx context.Context, subj Subject, m *Message) error {
	if s.markdown == nil || m.Body == "" {
		return nil
	}
	doc := code.Document{Org: subj.Org, Repo: subj.Repo, Ref: subj.Branch, Commit: subj.Head}
	switch subj.Kind() {
	case KindIssue:
		doc = code.Document{Org: subj.Org, Path: "issues/" + subj.Issue}
	case KindOrganization:
		doc = code.Document{Org: subj.Org}
	case KindAgent:
		doc = code.Document{Org: subj.Org, Path: "agents/" + subj.AgentID.String()}
	}
	html, err := s.markdown.Render(ctx, []byte(m.Body), doc)
	if err != nil {
		return fmt.Errorf("rendering message %s: %w", m.ID, err)
	}
	m.HTML = html
	return nil
}

// poster is the viewer as the poster of a message: an agent's messages link the task its token works.
func poster(ctx context.Context, v Viewer) Poster {
	task, _ := auth.WorkingTask(ctx, v.ID)
	return Poster{ID: v.ID, Task: task}
}

// orNew is id, or a new UUIDv7 for uuid.Nil.
func orNew(id uuid.UUID) (uuid.UUID, error) {
	if id != uuid.Nil {
		return id, nil
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("naming it: %w", err)
	}
	return id, nil
}

// stale reports whether a change that names version want is refused by a message or a thread at version got.
func stale(want, got int64) bool {
	return want > 0 && want != got
}

// failed records err on span, unless it is the viewer's to fix, and wraps it with what the service was doing.
func failed(span trace.Span, what string, err error) error {
	var verr *orgs.ValidationError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, repos.ErrNotFound), errors.As(err, &verr), errors.Is(err, auth.ErrForbidden), errors.Is(err, ErrChanged),
		errors.Is(err, ErrIDTaken), errors.Is(err, ErrDeleted), errors.Is(err, ErrBranchDeleted):
		return err
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, what)
	return fmt.Errorf("%s: %w", what, err)
}
