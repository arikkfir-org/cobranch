package chats

import (
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

// Store keeps the chats. Every change it makes appends the chat's event, whose subject is the chat's branch, and wakes
// the servers that follow it once its transaction commits; a change to the chat of a deleted branch is
// ErrBranchDeleted.
type Store interface {
	// Subject finds one of the repository's live branches by name as a chat's subject, or repos.ErrNotFound.
	Subject(ctx context.Context, repoID uuid.UUID, branch string) (Subject, error)
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
	// StartThread starts the subject's thread id with its first message, messageID, for actor, and answers it and true;
	// or, when a thread of id is there, answers it and false, and ErrIDTaken when it isn't the subject's.
	StartThread(ctx context.Context, actor uuid.UUID, subj Subject, id, messageID uuid.UUID, anchor *Anchor, body string) (Thread, bool, error)
	// Post posts message id to one of the subject's threads for actor, and answers it and true; or, when a message of
	// id is there, answers it and false, and ErrIDTaken when it isn't the thread's.
	Post(ctx context.Context, actor uuid.UUID, subj Subject, threadID, id uuid.UUID, body string) (Message, bool, error)
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
}

// Notifier tells the servers that follow a chat when it may have changed.
type Notifier interface {
	// Watch returns a channel that receives soon after a change to the chat of a subject, a branch, commits, and a
	// function that stops it. Changes that come while the channel is full make one receive.
	Watch(subjectID uuid.UUID) (<-chan struct{}, func())
}

// MaxUpdates is the most updates a read answers.
const MaxUpdates = 100

// poll is how often a follower reads its chat again without a wake, for a notification it missed.
const poll = time.Minute

// Service keeps the branches' chats for the viewers who read their repositories: every operation checks repo.read
// through the authorizer first, and answers repos.ErrNotFound without it, as for a repository that doesn't exist.
type Service struct {
	store    Store
	notifier Notifier
	markdown code.Renderer
	authz    *auth.Authorizer
}

// NewService returns a Service over store, rendering messages with markdown and checking permissions with authz;
// notifier wakes those who follow a chat. Without markdown, messages come as Markdown alone; without notifier, a
// follower reads its chat once a minute.
func NewService(store Store, notifier Notifier, markdown code.Renderer, authz *auth.Authorizer) *Service {
	return &Service{store: store, notifier: notifier, markdown: markdown, authz: authz}
}

// holds returns what v holds on the subject's repository, or repos.ErrNotFound without repo.read.
func (s *Service) holds(ctx context.Context, v Viewer, repoID uuid.UUID) ([]string, error) {
	held, err := s.authz.Holds(ctx, v.ID, auth.ScopeRepository, repoID)
	if err != nil {
		return nil, fmt.Errorf("reading the viewer's permissions: %w", err)
	}
	if !slices.Contains(held, repos.PermRead) {
		return nil, repos.ErrNotFound
	}
	return held, nil
}

// poster checks that v posts on the subject's repository, and returns what v holds there.
func (s *Service) poster(ctx context.Context, v Viewer, repoID uuid.UUID) ([]string, error) {
	held, err := s.holds(ctx, v, repoID)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(held, PermPost) {
		return nil, fmt.Errorf("%w: %s on the repository", auth.ErrForbidden, PermPost)
	}
	return held, nil
}

// on is the attributes of a span that acts on the subject's chat.
func on(subj Subject) []attribute.KeyValue {
	return []attribute.KeyValue{attribute.String("cobranch.repository.id", subj.RepoID.String()), attribute.String("cobranch.branch.id", subj.BranchID.String())}
}

// Branch finds the chat of one of the repository's live branches by name, without refs/heads/; it takes repo.read.
// A branch that isn't there is repos.ErrNotFound.
func (s *Service) Branch(ctx context.Context, v Viewer, repoID uuid.UUID, name string) (Subject, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Branch")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.branch", name))
	if _, err := s.holds(ctx, v, repoID); err != nil {
		return Subject{}, failed(span, "finding the chat", err)
	}
	subj, err := s.store.Subject(ctx, repoID, name)
	return subj, failed(span, "finding the chat", err)
}

// Threads lists a page of at most limit of the chat's threads, 1 to MaxPage, newest first, from the one before before
// names or from the newest when it is uuid.Nil, each with its messages; it takes repo.read.
func (s *Service) Threads(ctx context.Context, v Viewer, subj Subject, before uuid.UUID, limit int) (Page, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Threads", trace.WithAttributes(on(subj)...))
	defer span.End()
	if limit < 1 || limit > MaxPage {
		return Page{}, &orgs.ValidationError{Field: "limit", Code: "limit.invalid", Message: fmt.Sprintf("a page holds 1 to %d threads", MaxPage)}
	}
	if _, err := s.holds(ctx, v, subj.RepoID); err != nil {
		return Page{}, failed(span, "listing the threads", err)
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

// Thread finds one of the chat's threads with its messages; it takes repo.read. One that isn't there is
// repos.ErrNotFound.
func (s *Service) Thread(ctx context.Context, v Viewer, subj Subject, id uuid.UUID) (Thread, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Thread", trace.WithAttributes(on(subj)...))
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.chat.thread.id", id.String()))
	if _, err := s.holds(ctx, v, subj.RepoID); err != nil {
		return Thread{}, failed(span, "reading the thread", err)
	}
	t, err := s.store.Thread(ctx, subj, id)
	if err == nil {
		err = s.present(ctx, subj, &t)
	}
	return t, failed(span, "reading the thread", err)
}

// Message finds a message of one of the chat's threads; it takes repo.read. One that isn't there is
// repos.ErrNotFound.
func (s *Service) Message(ctx context.Context, v Viewer, subj Subject, threadID, id uuid.UUID) (Message, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Message", trace.WithAttributes(on(subj)...))
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.chat.message.id", id.String()))
	if _, err := s.holds(ctx, v, subj.RepoID); err != nil {
		return Message{}, failed(span, "reading the message", err)
	}
	m, err := s.message(ctx, subj, threadID, id)
	if err == nil {
		err = s.render(ctx, subj, &m)
	}
	return m, failed(span, "reading the message", err)
}

// StartThread starts a thread on the chat for the viewer with its first message, perhaps anchored to lines of a file at
// one of the repository's commits; it takes messages.post. Starting the viewer's thread again by its ID answers it as
// it is; an ID of another thread is ErrIDTaken.
func (s *Service) StartThread(ctx context.Context, v Viewer, subj Subject, in ThreadInput) (Thread, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.StartThread", trace.WithAttributes(on(subj)...))
	defer span.End()
	if _, err := s.poster(ctx, v, subj.RepoID); err != nil {
		return Thread{}, failed(span, "starting the thread", err)
	}
	if err := s.validateThread(ctx, subj, in); err != nil {
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
	t, created, err := s.store.StartThread(ctx, v.ID, subj, id, messageID, in.Anchor, in.Body)
	if err == nil && !created && (t.StartedBy == nil || t.StartedBy.ID != v.ID) {
		err = ErrIDTaken
	}
	if err == nil {
		err = s.present(ctx, subj, &t)
	}
	return t, failed(span, "starting the thread", err)
}

// validateThread reports whether in starts a thread: a UUIDv7 or no ID, a message, and an anchor, if any, at lines
// of a file at a commit the repository holds.
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

// Post posts a message to one of the chat's threads for the viewer; it takes messages.post. Posting the viewer's
// message to the thread again by its ID answers it as it is; an ID of another message is ErrIDTaken.
func (s *Service) Post(ctx context.Context, v Viewer, subj Subject, threadID uuid.UUID, in MessageInput) (Message, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Post", trace.WithAttributes(on(subj)...))
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.chat.thread.id", threadID.String()))
	if _, err := s.poster(ctx, v, subj.RepoID); err != nil {
		return Message{}, failed(span, "posting the message", err)
	}
	if err := validateID(in.ID); err != nil {
		return Message{}, failed(span, "posting the message", err)
	}
	if err := ValidateBody(in.Body); err != nil {
		return Message{}, failed(span, "posting the message", err)
	}
	id, err := orNew(in.ID)
	if err != nil {
		return Message{}, failed(span, "posting the message", err)
	}
	m, created, err := s.store.Post(ctx, v.ID, subj, threadID, id, in.Body)
	if err == nil && !created && (m.Author == nil || m.Author.ID != v.ID) {
		err = ErrIDTaken
	}
	if err == nil {
		err = s.render(ctx, subj, &m)
	}
	return m, failed(span, "posting the message", err)
}

// Edit changes the body of one of the viewer's messages in one of the chat's threads, at version or at any version
// for AnyVersion; it takes messages.post, and only a message's author edits it. A message at another version is
// ErrChanged, and a deleted one ErrDeleted. An edit that changes nothing answers the message as it is.
func (s *Service) Edit(ctx context.Context, v Viewer, subj Subject, threadID, id uuid.UUID, body string, version int64) (Message, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Edit", trace.WithAttributes(on(subj)...))
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.chat.message.id", id.String()))
	if _, err := s.poster(ctx, v, subj.RepoID); err != nil {
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
// for another's. A message at another version is ErrChanged; deleting a deleted one again changes nothing.
func (s *Service) Delete(ctx context.Context, v Viewer, subj Subject, threadID, id uuid.UUID, version int64) error {
	ctx, span := tracer.Start(ctx, "chats.Service.Delete", trace.WithAttributes(on(subj)...))
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.chat.message.id", id.String()))
	held, err := s.holds(ctx, v, subj.RepoID)
	if err != nil {
		return failed(span, "deleting the message", err)
	}
	m, err := s.message(ctx, subj, threadID, id)
	if err != nil {
		return failed(span, "deleting the message", err)
	}
	own := m.Author != nil && m.Author.ID == v.ID
	switch {
	case own && !slices.Contains(held, PermPost) && !slices.Contains(held, PermModerate):
		err = fmt.Errorf("%w: %s on the repository", auth.ErrForbidden, PermPost)
	case !own && !slices.Contains(held, PermModerate):
		err = fmt.Errorf("%w: %s on the repository, for another's message", auth.ErrForbidden, PermModerate)
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
// messages.moderate for another's. A thread at another version is ErrChanged; one already so is answered as it is.
func (s *Service) Resolve(ctx context.Context, v Viewer, subj Subject, id uuid.UUID, resolved bool, version int64) (Thread, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Resolve", trace.WithAttributes(on(subj)...))
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.chat.thread.id", id.String()), attribute.Bool("cobranch.chat.resolved", resolved))
	held, err := s.holds(ctx, v, subj.RepoID)
	if err != nil {
		return Thread{}, failed(span, "resolving the thread", err)
	}
	t, err := s.store.Thread(ctx, subj, id)
	if err != nil {
		return Thread{}, failed(span, "resolving the thread", err)
	}
	own := t.StartedBy != nil && t.StartedBy.ID == v.ID
	switch {
	case own && !slices.Contains(held, PermPost) && !slices.Contains(held, PermModerate):
		err = fmt.Errorf("%w: %s on the repository", auth.ErrForbidden, PermPost)
	case !own && !slices.Contains(held, PermModerate):
		err = fmt.Errorf("%w: %s on the repository, for another's thread", auth.ErrForbidden, PermModerate)
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

// message finds a message of the chat's thread, or repos.ErrNotFound.
func (s *Service) message(ctx context.Context, subj Subject, threadID, id uuid.UUID) (Message, error) {
	m, err := s.store.Message(ctx, subj, id)
	if err == nil && m.ThreadID != threadID {
		err = repos.ErrNotFound
	}
	return m, err
}

// Updates lists at most MaxUpdates of the chat's updates after cursor after, in order; it takes repo.read, checked at
// every read. With none yet, it waits up to wait for one, reading again whenever the chat may have changed.
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
// repository, then closes the channel; it takes repo.read, checked at every read. It reads whenever the chat may have
// changed, and once a minute for a change it missed.
func (s *Service) Follow(ctx context.Context, v Viewer, subj Subject, after int64) (<-chan Update, error) {
	ctx, span := tracer.Start(ctx, "chats.Service.Follow", trace.WithAttributes(on(subj)...))
	defer span.End()
	if _, err := s.holds(ctx, v, subj.RepoID); err != nil {
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
			if !errors.Is(err, repos.ErrNotFound) && ctx.Err() == nil {
				slog.ErrorContext(ctx, "Following a chat failed", "branch", subj.BranchID, "error", err)
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
	return s.notifier.Watch(subj.BranchID)
}

// read checks that v reads the repository, then lists at most MaxUpdates of the chat's updates after cursor after, at
// the branch's head now.
func (s *Service) read(ctx context.Context, v Viewer, subj Subject, after int64) ([]Update, error) {
	if _, err := s.holds(ctx, v, subj.RepoID); err != nil {
		return nil, err
	}
	head, err := s.store.Head(ctx, subj.BranchID)
	if err != nil {
		return nil, fmt.Errorf("reading the branch's head: %w", err)
	}
	subj.Head = head
	list, err := s.store.Updates(ctx, subj, after, MaxUpdates)
	if err != nil {
		return nil, fmt.Errorf("listing the chat's updates: %w", err)
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

// present works out whether t is outdated, and renders its messages.
func (s *Service) present(ctx context.Context, subj Subject, t *Thread) error {
	t.Outdated = t.Anchor != nil && t.Anchor.SHA != subj.Head
	for i := range t.Messages {
		if err := s.render(ctx, subj, &t.Messages[i]); err != nil {
			return err
		}
	}
	return nil
}

// render renders m's body as HTML, its relative links leading to the branch's files.
func (s *Service) render(ctx context.Context, subj Subject, m *Message) error {
	if s.markdown == nil || m.Body == "" {
		return nil
	}
	html, err := s.markdown.Render(ctx, []byte(m.Body), code.Document{Org: subj.Org, Repo: subj.Repo, Ref: subj.Branch, Commit: subj.Head})
	if err != nil {
		return fmt.Errorf("rendering message %s: %w", m.ID, err)
	}
	m.HTML = html
	return nil
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
