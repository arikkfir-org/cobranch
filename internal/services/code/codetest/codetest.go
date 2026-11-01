// Package codetest holds a fake of the code domain's port, for the tests of its service and of the adapters that
// serve it: a Store in memory.
package codetest

import (
	"context"
	"slices"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/repos"
)

// Store is a code.Store in memory: each repository's commits by name, branches, tags and pushes. Fail, when set,
// fails every listing.
type Store struct {
	Commits    map[uuid.UUID]map[string]code.Commit
	BranchList map[uuid.UUID][]code.Branch
	TagList    map[uuid.UUID][]code.Tag
	Pushes     map[uuid.UUID]code.Push
	Fail       error
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{Commits: map[uuid.UUID]map[string]code.Commit{}, BranchList: map[uuid.UUID][]code.Branch{}, TagList: map[uuid.UUID][]code.Tag{},
		Pushes: map[uuid.UUID]code.Push{}}
}

// Add records commits in the repository, as push brought them.
func (s *Store) Add(repoID uuid.UUID, push code.Push, commits ...code.Commit) {
	if s.Commits[repoID] == nil {
		s.Commits[repoID] = map[string]code.Commit{}
	}
	s.Pushes[push.ID] = push
	for _, c := range commits {
		c.FirstPush = push.ID
		s.Commits[repoID][c.SHA] = c
	}
}

// SetBranch makes the repository's branch name live at head, with its commit when the repository holds it.
func (s *Store) SetBranch(repoID uuid.UUID, name, head string) {
	b := code.Branch{Name: name, Head: head}
	if c, ok := s.Commits[repoID][head]; ok {
		b.Commit = &c
	}
	s.BranchList[repoID] = slices.DeleteFunc(s.BranchList[repoID], func(o code.Branch) bool { return o.Name == name })
	s.BranchList[repoID] = append(s.BranchList[repoID], b)
}

// Branches lists the repository's branches in the order they were set.
func (s *Store) Branches(_ context.Context, repoID uuid.UUID) ([]code.Branch, error) {
	if s.Fail != nil {
		return nil, s.Fail
	}
	return slices.Clone(s.BranchList[repoID]), nil
}

// Branch finds one of the repository's branches, or repos.ErrNotFound.
func (s *Store) Branch(_ context.Context, repoID uuid.UUID, name string) (code.Branch, error) {
	for _, b := range s.BranchList[repoID] {
		if b.Name == name {
			return b, nil
		}
	}
	return code.Branch{}, repos.ErrNotFound
}

// Tags lists the repository's tags in the order they were added.
func (s *Store) Tags(_ context.Context, repoID uuid.UUID) ([]code.Tag, error) {
	if s.Fail != nil {
		return nil, s.Fail
	}
	return slices.Clone(s.TagList[repoID]), nil
}

// FirstParents walks the repository's commits from sha along first parents.
func (s *Store) FirstParents(_ context.Context, repoID uuid.UUID, sha string, limit int) ([]code.Commit, error) {
	if s.Fail != nil {
		return nil, s.Fail
	}
	var out []code.Commit
	for c, ok := s.Commits[repoID][sha]; ok && len(out) < limit; c, ok = s.Commits[repoID][firstParent(c)] {
		out = append(out, c)
	}
	return out, nil
}

func firstParent(c code.Commit) string {
	if len(c.Parents) == 0 {
		return ""
	}
	return c.Parents[0]
}

// Commit finds one of the repository's commits with its push, or repos.ErrNotFound.
func (s *Store) Commit(_ context.Context, repoID uuid.UUID, sha string) (code.Commit, code.Push, error) {
	c, ok := s.Commits[repoID][sha]
	if !ok {
		return code.Commit{}, code.Push{}, repos.ErrNotFound
	}
	return c, s.Pushes[c.FirstPush], nil
}

// Line records commits named shas in the repository as push brought them, oldest first, each the first parent of the
// next, with message "Commit <its first letter>", and authors and committers whom pusher is behind.
func (s *Store) Line(repoID uuid.UUID, push code.Push, shas ...string) {
	for i, sha := range shas {
		who := code.Person{Name: "Pusher", Email: "pusher@example.com", At: push.At, Zone: "+0000", Principal: push.Pusher}
		c := code.Commit{SHA: sha, Tree: sha, Parents: []string{}, Message: "Commit " + sha[:1] + "\n", Author: who, Committer: who}
		if i > 0 {
			c.Parents = []string{shas[i-1]}
		}
		s.Add(repoID, push, c)
	}
}
