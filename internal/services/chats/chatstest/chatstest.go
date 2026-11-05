// Package chatstest holds fakes of the chats domain's ports, for the tests of its service and of the adapters that
// serve it: a Store in memory, and a Notifier the test wakes.
package chatstest

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/agents"
	"cobranch.dev/internal/services/agents/agentstest"
	"cobranch.dev/internal/services/chats"
	"cobranch.dev/internal/services/repos"
)

// Store is a chats.Store in memory: each repository's branches by name, the commits it holds, each organization's
// issues by number, the organizations, repositories and agents conversations are about, and the chats' threads,
// messages and updates. Names names the principals by ID, Tasks numbers
// the agents' tasks by ID, and TaskAgents names their agents; Now is the time it records changes at. Transact undoes
// what its function changed when it fails. It is safe for concurrent use, so a test may change it while a follower
// reads.
type Store struct {
	mu         sync.Mutex
	branches   map[uuid.UUID]map[string]chats.Subject
	issues     map[uuid.UUID]map[int]chats.Subject
	orgs       map[uuid.UUID]string
	repos      map[uuid.UUID]map[uuid.UUID]chats.Subject
	agents     map[uuid.UUID]map[uuid.UUID]string
	heads      map[uuid.UUID]string
	commits    map[uuid.UUID]map[string]bool
	threads    map[uuid.UUID]*thread
	messages   map[uuid.UUID]*chats.Message
	asks       map[uuid.UUID]uuid.UUID
	updates    []update
	Names      map[uuid.UUID]string
	Tasks      map[uuid.UUID]int64
	TaskAgents map[uuid.UUID]chats.Principal
	// Artifacts are the artifacts a message may name as its card, by ID.
	Artifacts map[uuid.UUID]chats.ArtifactRef
	Now       time.Time
}

type thread struct {
	subject uuid.UUID
	chats.Thread
}

type update struct {
	subject uuid.UUID
	cursor  int64
	kind    chats.UpdateKind
	thread  uuid.UUID
	message *uuid.UUID
}

var _ chats.Store = (*Store)(nil)

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{branches: map[uuid.UUID]map[string]chats.Subject{}, issues: map[uuid.UUID]map[int]chats.Subject{}, orgs: map[uuid.UUID]string{},
		repos: map[uuid.UUID]map[uuid.UUID]chats.Subject{}, agents: map[uuid.UUID]map[uuid.UUID]string{}, heads: map[uuid.UUID]string{}, commits: map[uuid.UUID]map[string]bool{},
		threads: map[uuid.UUID]*thread{}, messages: map[uuid.UUID]*chats.Message{}, asks: map[uuid.UUID]uuid.UUID{}, Names: map[uuid.UUID]string{},
		Tasks: map[uuid.UUID]int64{}, TaskAgents: map[uuid.UUID]chats.Principal{}, Artifacts: map[uuid.UUID]chats.ArtifactRef{}}
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

// SetIssue makes subj's issue one of its organization's, by its number, and answers subj with an issue ID when it had
// none.
func (s *Store) SetIssue(subj chats.Subject) chats.Subject {
	s.mu.Lock()
	defer s.mu.Unlock()
	if subj.IssueID == uuid.Nil {
		subj.IssueID = uuid.New()
	}
	if s.issues[subj.OrgID] == nil {
		s.issues[subj.OrgID] = map[int]chats.Subject{}
	}
	s.issues[subj.OrgID][subj.Number] = subj
	return subj
}

// NameTask tells the store an agent's task's number and agent, as the database's join reads them.
func (s *Store) NameTask(id uuid.UUID, number int64, agent chats.Principal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Tasks[id], s.TaskAgents[id] = number, agent
}

// AgentTasks is the agents' store as a chat records the tasks its messages ask for (chats.Tasks), which tells Chats
// each task's number and agent once it is assigned.
type AgentTasks struct {
	*agentstest.Store
	Chats *Store
}

// Assign assigns the task in the agents' store, and names it to Chats.
func (t AgentTasks) Assign(ctx context.Context, actor uuid.UUID, task agents.Task) (agents.Task, error) {
	out, err := t.Store.Assign(ctx, actor, task)
	if err != nil {
		return agents.Task{}, err //nolint:wrapcheck // the fake's own error, as the service reads it
	}
	t.Chats.NameTask(out.ID, out.Number, chats.Principal{ID: out.Agent.ID, DisplayName: out.Agent.DisplayName})
	return out, nil
}

// SetOrganization makes an organization of its ID and slug, which conversations may be about.
func (s *Store) SetOrganization(orgID uuid.UUID, slug string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.orgs[orgID] = slug
}

// SetRepository makes a repository of its organization, by its ID and slug, which conversations may be about; head is
// the commit its default branch is at, empty for none.
func (s *Store) SetRepository(orgID, repoID uuid.UUID, slug, head string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.repos[orgID] == nil {
		s.repos[orgID] = map[uuid.UUID]chats.Subject{}
	}
	s.repos[orgID][repoID] = chats.Subject{RepoID: repoID, Repo: slug, Branch: "main", Head: head}
}

// DeleteRepository deletes one of an organization's repositories, as a conversation's subject.
func (s *Store) DeleteRepository(orgID, repoID uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.repos[orgID], repoID)
}

// SetAgent makes an agent of its organization, by its principal and name, which conversations may be with.
func (s *Store) SetAgent(orgID, agentID uuid.UUID, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.agents[orgID] == nil {
		s.agents[orgID] = map[uuid.UUID]string{}
	}
	s.agents[orgID][agentID] = name
}

// DeleteIssue deletes an issue, as its chat's subject, by its organization and number.
func (s *Store) DeleteIssue(orgID uuid.UUID, number int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.issues[orgID], number)
}

// Move moves a branch to head.
func (s *Store) Move(branchID uuid.UUID, head string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.heads[branchID] = head
}

// Review makes a thread a comment of review id, which reviewed commit sha.
func (s *Store) Review(threadID, id uuid.UUID, sha string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threads[threadID].ReviewID, s.threads[threadID].ReviewedSHA = &id, sha
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

// Issue finds one of the organization's issues by number as a chat's subject, or repos.ErrNotFound.
func (s *Store) Issue(_ context.Context, orgID uuid.UUID, number int) (chats.Subject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subj, ok := s.issues[orgID][number]
	if !ok {
		return chats.Subject{}, repos.ErrNotFound
	}
	return subj, nil
}

// Conversation finds owner's private conversation about the organization, its repository repoID or with its agent
// agentID as a chat's subject, or repos.ErrNotFound.
func (s *Store) Conversation(_ context.Context, owner, orgID, repoID, agentID uuid.UUID) (chats.Subject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	slug, ok := s.orgs[orgID]
	if !ok {
		return chats.Subject{}, repos.ErrNotFound
	}
	subj := chats.Subject{OrgID: orgID, Org: slug, OwnerID: owner, ConversationID: chats.ConversationID(owner, orgID, repoID, agentID)}
	switch {
	case repoID != uuid.Nil:
		repo, ok := s.repos[orgID][repoID]
		if !ok {
			return chats.Subject{}, repos.ErrNotFound
		}
		subj.RepoID, subj.Repo, subj.Branch, subj.Head = repo.RepoID, repo.Repo, repo.Branch, repo.Head
	case agentID != uuid.Nil:
		name, ok := s.agents[orgID][agentID]
		if !ok {
			return chats.Subject{}, repos.ErrNotFound
		}
		subj.AgentID, subj.Agent = agentID, name
	}
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
		if t.subject == subj.ID() && (before == uuid.Nil || t.ID.String() < before.String()) {
			out = append(out, s.withMessages(t.Thread))
		}
	}
	slices.SortFunc(out, func(a, b chats.Thread) int { return cmp.Compare(b.ID.String(), a.ID.String()) })
	return out[:min(limit, len(out))], s.cursor(subj.ID()), nil
}

func (s *Store) cursor(subject uuid.UUID) int64 {
	var c int64
	for _, u := range s.updates {
		if u.subject == subject {
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
	if !ok || t.subject != subj.ID() {
		return chats.Thread{}, repos.ErrNotFound
	}
	return s.withMessages(t.Thread), nil
}

func (s *Store) withMessages(t chats.Thread) chats.Thread {
	t.Messages = []chats.Message{}
	for _, m := range s.messages {
		if m.ThreadID == t.ID {
			t.Messages = append(t.Messages, s.view(m))
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
	if !ok || s.threads[m.ThreadID].subject != subj.ID() {
		return chats.Message{}, repos.ErrNotFound
	}
	return s.view(m), nil
}

// view is m as a read answers it, with the task it asked for.
func (s *Store) view(m *chats.Message) chats.Message {
	out := *m
	if task, ok := s.asks[m.ID]; ok {
		if number, ok := s.Tasks[task]; ok {
			out.Asked = &chats.Asked{TaskRef: chats.TaskRef{ID: task, Number: number}, Agent: s.TaskAgents[task]}
		}
	}
	return out
}

// Transact runs fn, and undoes what it changed in the store when it fails.
func (s *Store) Transact(ctx context.Context, _ uuid.UUID, fn func(ctx context.Context) error) error {
	s.mu.Lock()
	threads, messages, asks, updates := map[uuid.UUID]*thread{}, map[uuid.UUID]*chats.Message{}, maps.Clone(s.asks), slices.Clone(s.updates)
	for id, t := range s.threads {
		c := *t
		threads[id] = &c
	}
	for id, m := range s.messages {
		c := *m
		messages[id] = &c
	}
	s.mu.Unlock()
	if err := fn(ctx); err != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.threads, s.messages, s.asks, s.updates = threads, messages, asks, updates
		return err
	}
	return nil
}

// principal is the principal of id, by its name.
func (s *Store) principal(id uuid.UUID) *chats.Principal {
	return &chats.Principal{ID: id, DisplayName: s.Names[id]}
}

// task is the task of id, by its number; nil for uuid.Nil.
func (s *Store) task(id uuid.UUID) *chats.TaskRef {
	if id == uuid.Nil {
		return nil
	}
	return &chats.TaskRef{ID: id, Number: s.Tasks[id]}
}

func (s *Store) record(subj chats.Subject, kind chats.UpdateKind, threadID uuid.UUID, messageID *uuid.UUID) {
	s.updates = append(s.updates, update{subject: subj.ID(), cursor: int64(len(s.updates) + 1), kind: kind, thread: threadID, message: messageID})
}

// StartThread starts the subject's thread id with its first message for by, or answers the thread of id that is
// there.
func (s *Store) StartThread(_ context.Context, by chats.Poster, subj chats.Subject, id, messageID uuid.UUID, anchor *chats.Anchor, said chats.Said) (chats.Thread, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.threads[id]; ok {
		if t.subject != subj.ID() {
			return chats.Thread{}, false, chats.ErrIDTaken
		}
		return s.withMessages(t.Thread), false, nil
	}
	s.threads[id] = &thread{subject: subj.ID(), Thread: chats.Thread{ID: id, Anchor: anchor, StartedBy: s.principal(by.ID), CreatedAt: s.Now, Version: 1}}
	s.messages[messageID] = &chats.Message{ID: messageID, ThreadID: id, Body: said.Body, Author: s.principal(by.ID), Task: s.task(by.Task),
		Artifact: s.card(said.ArtifactID), CreatedAt: s.Now, Version: 1}
	s.ask(messageID, by)
	s.record(subj, chats.ThreadStarted, id, &messageID)
	return s.withMessages(s.threads[id].Thread), true, nil
}

// card is the artifact id names as a message's card; nil for uuid.Nil.
func (s *Store) card(id uuid.UUID) *chats.ArtifactRef {
	a, ok := s.Artifacts[id]
	if !ok {
		return nil
	}
	return &a
}

// Artifact finds an artifact as a card names it, or repos.ErrNotFound (chats.Store).
func (s *Store) Artifact(_ context.Context, id uuid.UUID) (chats.ArtifactRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.Artifacts[id]
	if !ok {
		return chats.ArtifactRef{}, repos.ErrNotFound
	}
	return a, nil
}

// Post posts message id to one of the subject's threads for by, or answers the message of id that is there.
func (s *Store) Post(_ context.Context, by chats.Poster, subj chats.Subject, threadID, id uuid.UUID, said chats.Said) (chats.Message, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.threads[threadID]; !ok || t.subject != subj.ID() {
		return chats.Message{}, false, repos.ErrNotFound
	}
	if m, ok := s.messages[id]; ok {
		if m.ThreadID != threadID {
			return chats.Message{}, false, chats.ErrIDTaken
		}
		return s.view(m), false, nil
	}
	s.messages[id] = &chats.Message{ID: id, ThreadID: threadID, Body: said.Body, Author: s.principal(by.ID), Task: s.task(by.Task),
		Artifact: s.card(said.ArtifactID), CreatedAt: s.Now, Version: 1}
	s.ask(id, by)
	s.threads[threadID].Version++
	s.record(subj, chats.MessagePosted, threadID, &id)
	return s.view(s.messages[id]), true, nil
}

// ask records the task the message by posts asks for, if any.
func (s *Store) ask(messageID uuid.UUID, by chats.Poster) {
	if by.Asks != uuid.Nil {
		s.asks[messageID] = by.Asks
	}
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
	if !ok || t.subject != subj.ID() {
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
		if u.subject != subj.ID() || u.cursor <= after || len(out) == limit {
			continue
		}
		next := chats.Update{Cursor: u.cursor, Kind: u.kind, Thread: s.threads[u.thread].Thread}
		if u.message != nil {
			m := s.view(s.messages[*u.message])
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
