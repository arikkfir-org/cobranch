// Package chatstest holds fakes of the chats domain's ports, for the tests of its service and of the adapters that
// serve it: a Store in memory, and a Notifier the test wakes.
package chatstest

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/chats"
	"cobranch.dev/internal/services/repos"
)

// Store is a chats.Store in memory: each repository's branches by name, the commits it holds, and the chats' threads,
// messages and updates. Names names the principals by ID; Now is the time it records changes at. It is safe for
// concurrent use, so a test may change it while a follower reads.
type Store struct {
	mu       sync.Mutex
	branches map[uuid.UUID]map[string]chats.Subject
	heads    map[uuid.UUID]string
	commits  map[uuid.UUID]map[string]bool
	threads  map[uuid.UUID]*thread
	messages map[uuid.UUID]*chats.Message
	updates  []update
	Names    map[uuid.UUID]string
	Now      time.Time
}

type thread struct {
	branch uuid.UUID
	chats.Thread
}

type update struct {
	branch  uuid.UUID
	cursor  int64
	kind    chats.UpdateKind
	thread  uuid.UUID
	message *uuid.UUID
}

var _ chats.Store = (*Store)(nil)

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{branches: map[uuid.UUID]map[string]chats.Subject{}, heads: map[uuid.UUID]string{}, commits: map[uuid.UUID]map[string]bool{},
		threads: map[uuid.UUID]*thread{}, messages: map[uuid.UUID]*chats.Message{}, Names: map[uuid.UUID]string{}}
}

// SetBranch makes subj's branch live at its head in its repository, and the repository hold the head, and answers
// subj with a branch ID when it had none.
func (s *Store) SetBranch(subj chats.Subject) chats.Subject {
	s.mu.Lock()
	defer s.mu.Unlock()
	if subj.BranchID == uuid.Nil {
		subj.BranchID = uuid.New()
	}
	if s.branches[subj.RepoID] == nil {
		s.branches[subj.RepoID] = map[string]chats.Subject{}
	}
	s.branches[subj.RepoID][subj.Branch] = subj
	s.heads[subj.BranchID] = subj.Head
	s.addCommit(subj.RepoID, subj.Head)
	return subj
}

// Move moves a branch to head.
func (s *Store) Move(branchID uuid.UUID, head string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.heads[branchID] = head
}

// AddCommit makes the repository hold a commit.
func (s *Store) AddCommit(repoID uuid.UUID, sha string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addCommit(repoID, sha)
}

func (s *Store) addCommit(repoID uuid.UUID, sha string) {
	if s.commits[repoID] == nil {
		s.commits[repoID] = map[string]bool{}
	}
	s.commits[repoID][sha] = true
}

// Subject finds a repository's branch by name as a chat's subject, or repos.ErrNotFound.
func (s *Store) Subject(_ context.Context, repoID uuid.UUID, branch string) (chats.Subject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subj, ok := s.branches[repoID][branch]
	if !ok {
		return chats.Subject{}, repos.ErrNotFound
	}
	subj.Head = s.heads[subj.BranchID]
	return subj, nil
}

// Head finds the object a branch is at now, or repos.ErrNotFound.
func (s *Store) Head(_ context.Context, branchID uuid.UUID) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	head, ok := s.heads[branchID]
	if !ok {
		return "", repos.ErrNotFound
	}
	return head, nil
}

// HasCommit reports whether the repository holds a commit.
func (s *Store) HasCommit(_ context.Context, repoID uuid.UUID, sha string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commits[repoID][sha], nil
}

// Threads lists at most limit of the subject's threads, newest first, from the one before before names, each with its
// messages, and the chat's cursor.
func (s *Store) Threads(_ context.Context, subj chats.Subject, before uuid.UUID, limit int) ([]chats.Thread, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []chats.Thread
	for _, t := range s.threads {
		if t.branch == subj.BranchID && (before == uuid.Nil || t.ID.String() < before.String()) {
			out = append(out, s.withMessages(t.Thread))
		}
	}
	slices.SortFunc(out, func(a, b chats.Thread) int { return cmp.Compare(b.ID.String(), a.ID.String()) })
	return out[:min(limit, len(out))], s.cursor(subj.BranchID), nil
}

func (s *Store) cursor(branch uuid.UUID) int64 {
	var c int64
	for _, u := range s.updates {
		if u.branch == branch {
			c = u.cursor
		}
	}
	return c
}

// Thread finds one of the subject's threads with its messages, or repos.ErrNotFound.
func (s *Store) Thread(_ context.Context, subj chats.Subject, id uuid.UUID) (chats.Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.threads[id]
	if !ok || t.branch != subj.BranchID {
		return chats.Thread{}, repos.ErrNotFound
	}
	return s.withMessages(t.Thread), nil
}

func (s *Store) withMessages(t chats.Thread) chats.Thread {
	t.Messages = []chats.Message{}
	for _, m := range s.messages {
		if m.ThreadID == t.ID {
			t.Messages = append(t.Messages, *m)
		}
	}
	slices.SortFunc(t.Messages, func(a, b chats.Message) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID.String(), b.ID.String()))
	})
	return t
}

// Message finds a message of one of the subject's threads, or repos.ErrNotFound.
func (s *Store) Message(_ context.Context, subj chats.Subject, id uuid.UUID) (chats.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.message(subj, id)
}

func (s *Store) message(subj chats.Subject, id uuid.UUID) (chats.Message, error) {
	m, ok := s.messages[id]
	if !ok || s.threads[m.ThreadID].branch != subj.BranchID {
		return chats.Message{}, repos.ErrNotFound
	}
	return *m, nil
}

// principal is the principal of id, by its name.
func (s *Store) principal(id uuid.UUID) *chats.Principal {
	return &chats.Principal{ID: id, DisplayName: s.Names[id]}
}

func (s *Store) record(subj chats.Subject, kind chats.UpdateKind, threadID uuid.UUID, messageID *uuid.UUID) {
	s.updates = append(s.updates, update{branch: subj.BranchID, cursor: int64(len(s.updates) + 1), kind: kind, thread: threadID, message: messageID})
}

// StartThread starts the subject's thread id with its first message for actor, or answers the thread of id that is
// there.
func (s *Store) StartThread(_ context.Context, actor uuid.UUID, subj chats.Subject, id, messageID uuid.UUID, anchor *chats.Anchor, body string) (chats.Thread, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.threads[id]; ok {
		if t.branch != subj.BranchID {
			return chats.Thread{}, false, chats.ErrIDTaken
		}
		return s.withMessages(t.Thread), false, nil
	}
	s.threads[id] = &thread{branch: subj.BranchID, Thread: chats.Thread{ID: id, Anchor: anchor, StartedBy: s.principal(actor), CreatedAt: s.Now, Version: 1}}
	s.messages[messageID] = &chats.Message{ID: messageID, ThreadID: id, Body: body, Author: s.principal(actor), CreatedAt: s.Now, Version: 1}
	s.record(subj, chats.ThreadStarted, id, &messageID)
	return s.withMessages(s.threads[id].Thread), true, nil
}

// Post posts message id to one of the subject's threads for actor, or answers the message of id that is there.
func (s *Store) Post(_ context.Context, actor uuid.UUID, subj chats.Subject, threadID, id uuid.UUID, body string) (chats.Message, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.threads[threadID]; !ok || t.branch != subj.BranchID {
		return chats.Message{}, false, repos.ErrNotFound
	}
	if m, ok := s.messages[id]; ok {
		if m.ThreadID != threadID {
			return chats.Message{}, false, chats.ErrIDTaken
		}
		return *m, false, nil
	}
	s.messages[id] = &chats.Message{ID: id, ThreadID: threadID, Body: body, Author: s.principal(actor), CreatedAt: s.Now, Version: 1}
	s.threads[threadID].Version++
	s.record(subj, chats.MessagePosted, threadID, &id)
	return *s.messages[id], true, nil
}

// Edit changes the body of a live message of the subject's, at version or at any version, or chats.ErrChanged.
func (s *Store) Edit(_ context.Context, _ uuid.UUID, subj chats.Subject, id uuid.UUID, version int64, body string) (chats.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.message(subj, id); err != nil {
		return chats.Message{}, err
	}
	m := s.messages[id]
	if m.DeletedAt != nil || version >= 0 && version != m.Version {
		return chats.Message{}, chats.ErrChanged
	}
	at := s.Now
	m.Body, m.EditedAt, m.Version = body, &at, m.Version+1
	s.threads[m.ThreadID].Version++
	s.record(subj, chats.MessageEdited, m.ThreadID, &id)
	return *m, nil
}

// Delete deletes a live message of the subject's for actor, at version or at any version, or chats.ErrChanged.
func (s *Store) Delete(_ context.Context, actor uuid.UUID, subj chats.Subject, id uuid.UUID, version int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.message(subj, id); err != nil {
		return err
	}
	m := s.messages[id]
	if m.DeletedAt != nil || version >= 0 && version != m.Version {
		return chats.ErrChanged
	}
	at := s.Now
	m.Body, m.DeletedAt, m.DeletedBy, m.Version = "", &at, s.principal(actor), m.Version+1
	s.threads[m.ThreadID].Version++
	s.record(subj, chats.MessageDeleted, m.ThreadID, &id)
	return nil
}

// Resolve resolves one of the subject's threads for actor, or opens it again, at version or at any version, or
// chats.ErrChanged.
func (s *Store) Resolve(_ context.Context, actor uuid.UUID, subj chats.Subject, id uuid.UUID, version int64, resolved bool) (chats.Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.threads[id]
	if !ok || t.branch != subj.BranchID {
		return chats.Thread{}, repos.ErrNotFound
	}
	if version >= 0 && version != t.Version {
		return chats.Thread{}, chats.ErrChanged
	}
	kind := chats.ThreadReopened
	t.ResolvedAt, t.ResolvedBy = nil, nil
	if resolved {
		at := s.Now
		kind, t.ResolvedAt, t.ResolvedBy = chats.ThreadResolved, &at, s.principal(actor)
	}
	t.Version++
	s.record(subj, kind, id, nil)
	return s.withMessages(t.Thread), nil
}

// Updates lists at most limit of the subject's chat updates after cursor after, in order.
func (s *Store) Updates(_ context.Context, subj chats.Subject, after int64, limit int) ([]chats.Update, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []chats.Update
	for _, u := range s.updates {
		if u.branch != subj.BranchID || u.cursor <= after || len(out) == limit {
			continue
		}
		next := chats.Update{Cursor: u.cursor, Kind: u.kind, Thread: s.threads[u.thread].Thread}
		if u.message != nil {
			m := *s.messages[*u.message]
			next.Message = &m
		}
		out = append(out, next)
	}
	return out, nil
}

// Notifier is a chats.Notifier that the test wakes.
type Notifier struct {
	mu       sync.Mutex
	watchers map[uuid.UUID][]chan struct{}
}

var _ chats.Notifier = (*Notifier)(nil)

// Watch returns a channel that Wake wakes for the subject.
func (n *Notifier) Watch(subjectID uuid.UUID) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.watchers == nil {
		n.watchers = map[uuid.UUID][]chan struct{}{}
	}
	n.watchers[subjectID] = append(n.watchers[subjectID], ch)
	return ch, func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		n.watchers[subjectID] = slices.DeleteFunc(n.watchers[subjectID], func(c chan struct{}) bool { return c == ch })
	}
}

// Wake wakes those who watch the subject.
func (n *Notifier) Wake(subjectID uuid.UUID) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, ch := range n.watchers[subjectID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Watching counts those who watch the subject.
func (n *Notifier) Watching(subjectID uuid.UUID) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.watchers[subjectID])
}
