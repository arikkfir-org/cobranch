package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/chats"
	"cobranch.dev/internal/services/repos"
)

// ChatStore keeps the branches' chats (db/migrations/00022_chats.sql): chats.Store on PostgreSQL, through the queries
// sqlc generates from db/queries. Each change runs in a transaction that acts for its principal, holds the chat's lock,
// appends the chat's event and notifies the servers that listen.
type ChatStore struct {
	pool *pgxpool.Pool
	q    *queries.Queries
}

// NewChatStore returns the chats' store on pool's database.
func NewChatStore(pool *pgxpool.Pool) *ChatStore {
	return &ChatStore{pool: pool, q: queries.New(pool)}
}

var _ chats.Store = (*ChatStore)(nil)

// chatEvent is the details of a chat's event: the thread it changed and the message, if any.
type chatEvent struct {
	Thread  uuid.UUID  `json:"thread"`
	Message *uuid.UUID `json:"message,omitempty"`
}

// Subject finds one of the repository's live branches by name as a chat's subject, or repos.ErrNotFound (chats.Store).
func (s *ChatStore) Subject(ctx context.Context, repoID uuid.UUID, branch string) (chats.Subject, error) {
	row, err := s.q.ChatSubject(ctx, repoID, branch)
	if errors.Is(err, pgx.ErrNoRows) {
		return chats.Subject{}, repos.ErrNotFound
	}
	if err != nil {
		return chats.Subject{}, fmt.Errorf("finding branch %s: %w", branch, err)
	}
	return chats.Subject{OrgID: row.OrgID, RepoID: row.RepoID, BranchID: row.ID, Branch: row.Name, Head: row.Head, Org: row.OrgSlug,
		Repo: row.RepoSlug}, nil
}

// Head finds the object a branch is at now, deleted or not, or repos.ErrNotFound (chats.Store).
func (s *ChatStore) Head(ctx context.Context, branchID uuid.UUID) (string, error) {
	head, err := s.q.BranchHead(ctx, branchID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", repos.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("finding branch %s: %w", branchID, err)
	}
	return head, nil
}

// HasCommit reports whether the repository holds a commit (chats.Store).
func (s *ChatStore) HasCommit(ctx context.Context, repoID uuid.UUID, sha string) (bool, error) {
	held, err := s.q.HasCommit(ctx, repoID, sha)
	if err != nil {
		return false, fmt.Errorf("finding commit %s: %w", sha, err)
	}
	return held, nil
}

// Threads lists at most limit of the subject's threads, newest first, from the one before before names, each with its
// messages, and the cursor of the chat's updates, read first (chats.Store).
func (s *ChatStore) Threads(ctx context.Context, subj chats.Subject, before uuid.UUID, limit int) ([]chats.Thread, int64, error) {
	after, err := s.q.ChatCursor(ctx, &subj.BranchID)
	if err != nil {
		return nil, 0, fmt.Errorf("reading the chat's cursor: %w", err)
	}
	p := queries.ChatThreadsParams{RepoID: subj.RepoID, BranchID: subj.BranchID, Lim: int32(limit)} //nolint:gosec // a page is at most chats.MaxPage + 1
	if before != uuid.Nil {
		p.Before = &before
	}
	rows, err := s.q.ChatThreads(ctx, p)
	if err != nil {
		return nil, 0, fmt.Errorf("listing the threads: %w", err)
	}
	list := make([]chats.Thread, len(rows))
	for i, r := range rows {
		list[i] = chatThreadOf(r.ChatThread, r.StarterName, r.ResolverName)
	}
	if err := withMessages(ctx, s.q, list); err != nil {
		return nil, 0, err
	}
	return list, after, nil
}

// Thread finds one of the subject's threads with its messages, or repos.ErrNotFound (chats.Store).
func (s *ChatStore) Thread(ctx context.Context, subj chats.Subject, id uuid.UUID) (chats.Thread, error) {
	return chatThread(ctx, s.q, subj, id)
}

// chatThread finds one of the subject's threads with its messages, or repos.ErrNotFound.
func chatThread(ctx context.Context, q *queries.Queries, subj chats.Subject, id uuid.UUID) (chats.Thread, error) {
	threads, err := chatThreads(ctx, q, subj, []uuid.UUID{id})
	if err != nil {
		return chats.Thread{}, err
	}
	t, found := threads[id]
	if !found {
		return chats.Thread{}, repos.ErrNotFound
	}
	list := []chats.Thread{t}
	if err := withMessages(ctx, q, list); err != nil {
		return chats.Thread{}, err
	}
	return list[0], nil
}

// chatThreads finds those of the subject's threads that ids name, without their messages, by ID.
func chatThreads(ctx context.Context, q *queries.Queries, subj chats.Subject, ids []uuid.UUID) (map[uuid.UUID]chats.Thread, error) {
	rows, err := q.ChatThreadsByID(ctx, subj.RepoID, subj.BranchID, ids)
	if err != nil {
		return nil, fmt.Errorf("finding the threads: %w", err)
	}
	out := make(map[uuid.UUID]chats.Thread, len(rows))
	for _, r := range rows {
		out[r.ChatThread.ID] = chatThreadOf(r.ChatThread, r.StarterName, r.ResolverName)
	}
	return out, nil
}

// withMessages fills in the messages of threads, in the order they were posted.
func withMessages(ctx context.Context, q *queries.Queries, threads []chats.Thread) error {
	if len(threads) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(threads))
	at := make(map[uuid.UUID]int, len(threads))
	for i, t := range threads {
		ids[i], at[t.ID] = t.ID, i
		threads[i].Messages = []chats.Message{}
	}
	rows, err := q.ChatMessagesOf(ctx, ids)
	if err != nil {
		return fmt.Errorf("listing the messages: %w", err)
	}
	for _, r := range rows {
		i := at[r.ChatMessage.ThreadID]
		threads[i].Messages = append(threads[i].Messages, chatMessageOf(r.ChatMessage, r.AuthorName, r.DeleterName))
	}
	return nil
}

// Message finds a message of one of the subject's threads, or repos.ErrNotFound (chats.Store).
func (s *ChatStore) Message(ctx context.Context, subj chats.Subject, id uuid.UUID) (chats.Message, error) {
	return chatMessage(ctx, s.q, subj, id)
}

// chatMessage finds a message of one of the subject's threads, or repos.ErrNotFound.
func chatMessage(ctx context.Context, q *queries.Queries, subj chats.Subject, id uuid.UUID) (chats.Message, error) {
	messages, err := chatMessages(ctx, q, subj, []uuid.UUID{id})
	if err != nil {
		return chats.Message{}, err
	}
	m, found := messages[id]
	if !found {
		return chats.Message{}, repos.ErrNotFound
	}
	return m, nil
}

// chatMessages finds those of the messages ids name that are of the subject's threads, by ID.
func chatMessages(ctx context.Context, q *queries.Queries, subj chats.Subject, ids []uuid.UUID) (map[uuid.UUID]chats.Message, error) {
	rows, err := q.ChatMessagesByID(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("finding the messages: %w", err)
	}
	threadIDs := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		threadIDs = append(threadIDs, r.ChatMessage.ThreadID)
	}
	threads, err := chatThreads(ctx, q, subj, threadIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]chats.Message, len(rows))
	for _, r := range rows {
		if _, ours := threads[r.ChatMessage.ThreadID]; ours {
			out[r.ChatMessage.ID] = chatMessageOf(r.ChatMessage, r.AuthorName, r.DeleterName)
		}
	}
	return out, nil
}

// changeChat runs fn in a transaction that acts for actor and holds the subject's chat, whose branch must still live:
// chats.ErrBranchDeleted otherwise. Unless fn changed nothing, it
// then appends the chat's event of the kind fn answers, about the thread and the message, if any, and notifies the
// servers that listen.
func (s *ChatStore) changeChat(ctx context.Context, actor uuid.UUID, subj chats.Subject, what string, fn func(ctx context.Context, q *queries.Queries) (chats.UpdateKind, chatEvent, error)) error {
	return transact(ctx, s.pool, s.q, what, actor, func(ctx context.Context, q *queries.Queries) error {
		if err := q.LockChat(ctx, subj.BranchID.String()); err != nil {
			return fmt.Errorf("locking the chat: %w", err)
		}
		if deleted, err := q.BranchDeleted(ctx, subj.BranchID); errors.Is(err, pgx.ErrNoRows) {
			return repos.ErrNotFound
		} else if err != nil {
			return fmt.Errorf("finding the chat's branch: %w", err)
		} else if deleted {
			return chats.ErrBranchDeleted
		}
		kind, e, err := fn(ctx, q)
		if err != nil || kind == "" {
			return err
		}
		details := map[string]any{"repo": subj.RepoID, "thread": e.Thread}
		if e.Message != nil {
			details["message"] = *e.Message
		}
		if err := orgEvent(ctx, q, &subj.OrgID, actor, "chat."+string(kind), subj.BranchID, details); err != nil {
			return err
		}
		if err := q.NotifyChat(ctx, subj.BranchID.String()); err != nil {
			return fmt.Errorf("notifying the chat's followers: %w", err)
		}
		return nil
	})
}

// StartThread starts the subject's thread id with its first message for actor, and answers it and true; or answers
// the thread of id that is there and false, and chats.ErrIDTaken when it isn't the subject's (chats.Store).
func (s *ChatStore) StartThread(ctx context.Context, actor uuid.UUID, subj chats.Subject, id, messageID uuid.UUID, anchor *chats.Anchor, body string) (chats.Thread, bool, error) {
	var out chats.Thread
	created := false
	err := s.changeChat(ctx, actor, subj, "starting the thread", func(ctx context.Context, q *queries.Queries) (chats.UpdateKind, chatEvent, error) {
		p := queries.InsertChatThreadParams{ID: id, OrgID: subj.OrgID, RepoID: subj.RepoID, BranchID: subj.BranchID, CreatedBy: &actor}
		if anchor != nil {
			start, end := int32(anchor.StartLine), int32(anchor.EndLine) //nolint:gosec // lines a file holds fit
			p.Sha, p.Path, p.StartLine, p.EndLine = &anchor.SHA, &anchor.Path, &start, &end
		}
		_, err := q.InsertChatThread(ctx, p)
		if errors.Is(err, pgx.ErrNoRows) { // one of its ID is there
			out, err = chatThread(ctx, q, subj, id)
			if errors.Is(err, repos.ErrNotFound) {
				err = chats.ErrIDTaken
			}
			return "", chatEvent{}, err
		}
		if err != nil {
			return "", chatEvent{}, fmt.Errorf("inserting thread %s: %w", id, err)
		}
		if _, err := q.InsertChatMessage(ctx, queries.InsertChatMessageParams{ID: messageID, ThreadID: id, OrgID: subj.OrgID, AuthorID: &actor,
			Body: body}); err != nil {
			return "", chatEvent{}, fmt.Errorf("inserting message %s: %w", messageID, err)
		}
		created = true
		out, err = chatThread(ctx, q, subj, id)
		return chats.ThreadStarted, chatEvent{Thread: id, Message: &messageID}, err
	})
	return out, created, err
}

// Post posts message id to one of the subject's threads for actor, and answers it and true; or answers the message of
// id that is there and false, and chats.ErrIDTaken when it isn't the thread's (chats.Store).
func (s *ChatStore) Post(ctx context.Context, actor uuid.UUID, subj chats.Subject, threadID, id uuid.UUID, body string) (chats.Message, bool, error) {
	var out chats.Message
	created := false
	err := s.changeChat(ctx, actor, subj, "posting the message", func(ctx context.Context, q *queries.Queries) (chats.UpdateKind, chatEvent, error) {
		threads, err := chatThreads(ctx, q, subj, []uuid.UUID{threadID})
		if err != nil {
			return "", chatEvent{}, err
		}
		if _, found := threads[threadID]; !found {
			return "", chatEvent{}, repos.ErrNotFound
		}
		_, err = q.InsertChatMessage(ctx, queries.InsertChatMessageParams{ID: id, ThreadID: threadID, OrgID: subj.OrgID, AuthorID: &actor, Body: body})
		if errors.Is(err, pgx.ErrNoRows) { // one of its ID is there
			out, err = chatMessage(ctx, q, subj, id)
			if errors.Is(err, repos.ErrNotFound) || err == nil && out.ThreadID != threadID {
				err = chats.ErrIDTaken
			}
			return "", chatEvent{}, err
		}
		if err != nil {
			return "", chatEvent{}, fmt.Errorf("inserting message %s: %w", id, err)
		}
		created = true
		out, err = chatMessage(ctx, q, subj, id)
		return chats.MessagePosted, chatEvent{Thread: threadID, Message: &id}, err
	})
	return out, created, err
}

// Edit changes the body of a live message of the subject's, at version or at any version for chats.AnyVersion, or
// chats.ErrChanged (chats.Store).
func (s *ChatStore) Edit(ctx context.Context, actor uuid.UUID, subj chats.Subject, id uuid.UUID, version int64, body string) (chats.Message, error) {
	var out chats.Message
	err := s.changeChat(ctx, actor, subj, "editing the message", func(ctx context.Context, q *queries.Queries) (chats.UpdateKind, chatEvent, error) {
		if _, err := chatMessage(ctx, q, subj, id); err != nil {
			return "", chatEvent{}, err
		}
		row, err := q.EditChatMessage(ctx, body, id, version)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", chatEvent{}, chats.ErrChanged
		}
		if err != nil {
			return "", chatEvent{}, fmt.Errorf("editing message %s: %w", id, err)
		}
		out, err = chatMessage(ctx, q, subj, id)
		return chats.MessageEdited, chatEvent{Thread: row.ThreadID, Message: &id}, err
	})
	return out, err
}

// Delete deletes a live message of the subject's for actor, at version or at any version for chats.AnyVersion, or
// chats.ErrChanged (chats.Store).
func (s *ChatStore) Delete(ctx context.Context, actor uuid.UUID, subj chats.Subject, id uuid.UUID, version int64) error {
	return s.changeChat(ctx, actor, subj, "deleting the message", func(ctx context.Context, q *queries.Queries) (chats.UpdateKind, chatEvent, error) {
		if _, err := chatMessage(ctx, q, subj, id); err != nil {
			return "", chatEvent{}, err
		}
		row, err := q.DeleteChatMessage(ctx, &actor, id, version)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", chatEvent{}, chats.ErrChanged
		}
		if err != nil {
			return "", chatEvent{}, fmt.Errorf("deleting message %s: %w", id, err)
		}
		return chats.MessageDeleted, chatEvent{Thread: row.ThreadID, Message: &id}, nil
	})
}

// Resolve resolves one of the subject's threads for actor, or opens it again, at version or at any version for
// chats.AnyVersion, and answers it with its messages, or chats.ErrChanged (chats.Store).
func (s *ChatStore) Resolve(ctx context.Context, actor uuid.UUID, subj chats.Subject, id uuid.UUID, version int64, resolved bool) (chats.Thread, error) {
	var out chats.Thread
	err := s.changeChat(ctx, actor, subj, "resolving the thread", func(ctx context.Context, q *queries.Queries) (chats.UpdateKind, chatEvent, error) {
		if _, err := chatThread(ctx, q, subj, id); err != nil {
			return "", chatEvent{}, err
		}
		var by *uuid.UUID
		kind := chats.ThreadReopened
		if resolved {
			by, kind = &actor, chats.ThreadResolved
		}
		if _, err := q.ResolveChatThread(ctx, by, id, version); errors.Is(err, pgx.ErrNoRows) {
			return "", chatEvent{}, chats.ErrChanged
		} else if err != nil {
			return "", chatEvent{}, fmt.Errorf("resolving thread %s: %w", id, err)
		}
		var err error
		out, err = chatThread(ctx, q, subj, id)
		return kind, chatEvent{Thread: id}, err
	})
	return out, err
}

// Updates lists at most limit of the subject's chat updates after cursor after, in order, each with its thread and
// message as they are now (chats.Store).
func (s *ChatStore) Updates(ctx context.Context, subj chats.Subject, after int64, limit int) ([]chats.Update, error) {
	events, err := s.q.ChatEvents(ctx, &subj.BranchID, after, int32(limit)) //nolint:gosec // a read is at most chats.MaxUpdates
	if err != nil {
		return nil, fmt.Errorf("listing the chat's events: %w", err)
	}
	details := make([]chatEvent, len(events))
	var threadIDs, messageIDs []uuid.UUID
	for i, e := range events {
		if err := json.Unmarshal(e.Details, &details[i]); err != nil {
			return nil, fmt.Errorf("reading event %d: %w", e.Seq, err)
		}
		threadIDs = append(threadIDs, details[i].Thread)
		if details[i].Message != nil {
			messageIDs = append(messageIDs, *details[i].Message)
		}
	}
	threads, err := chatThreads(ctx, s.q, subj, threadIDs)
	if err != nil {
		return nil, err
	}
	messages, err := chatMessages(ctx, s.q, subj, messageIDs)
	if err != nil {
		return nil, err
	}
	out := make([]chats.Update, 0, len(events))
	for i, e := range events {
		u := chats.Update{Cursor: e.Seq, Kind: chats.UpdateKind(e.Kind[len("chat."):]), Thread: threads[details[i].Thread]}
		if id := details[i].Message; id != nil {
			if m, found := messages[*id]; found {
				u.Message = &m
			}
		}
		out = append(out, u)
	}
	return out, nil
}

// chatThreadOf is the domain's view of a chat_threads row, with the display names of its starter and resolver.
func chatThreadOf(t queries.ChatThread, starter, resolver *string) chats.Thread {
	out := chats.Thread{ID: t.ID, StartedBy: principalOf(t.CreatedBy, starter), CreatedAt: t.CreatedAt, ResolvedAt: t.ResolvedAt,
		ResolvedBy: principalOf(t.ResolvedBy, resolver), Version: t.Version}
	if t.Sha != nil && t.Path != nil && t.StartLine != nil && t.EndLine != nil {
		out.Anchor = &chats.Anchor{SHA: *t.Sha, Path: *t.Path, StartLine: int(*t.StartLine), EndLine: int(*t.EndLine)}
	}
	return out
}

// chatMessageOf is the domain's view of a chat_messages row, with the display names of its author and deleter.
func chatMessageOf(m queries.ChatMessage, author, deleter *string) chats.Message {
	return chats.Message{ID: m.ID, ThreadID: m.ThreadID, Body: m.Body, Author: principalOf(m.AuthorID, author), CreatedAt: m.CreatedAt,
		EditedAt: m.EditedAt, DeletedAt: m.DeletedAt, DeletedBy: principalOf(m.DeletedBy, deleter), Version: m.Version}
}
