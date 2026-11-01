// Package codetest holds a fake of the code domain's port, for the tests of its service and of the adapters that
// serve it: a Store in memory.
package codetest

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/repos"
)

// Store is a code.Store in memory: each repository's commits by name, branches and pushes, and the annotations on its
// commits. Fail, when set, fails every listing and every change. Names names the principals who set annotations, by
// ID; Now is the time it records changes at.
type Store struct {
	Commits    map[uuid.UUID]map[string]code.Commit
	BranchList map[uuid.UUID][]code.Branch
	Pushes     map[uuid.UUID]code.Push
	Notes      map[uuid.UUID]map[string][]code.Annotation
	Names      map[uuid.UUID]string
	Now        time.Time
	Fail       error
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{Commits: map[uuid.UUID]map[string]code.Commit{}, BranchList: map[uuid.UUID][]code.Branch{}, Pushes: map[uuid.UUID]code.Push{},
		Notes: map[uuid.UUID]map[string][]code.Annotation{}, Names: map[uuid.UUID]string{}}
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
	out := slices.Clone(s.BranchList[repoID])
	for i := range out {
		out[i] = s.annotatedBranch(repoID, out[i])
	}
	return out, nil
}

// Branch finds one of the repository's branches, or repos.ErrNotFound.
func (s *Store) Branch(_ context.Context, repoID uuid.UUID, name string) (code.Branch, error) {
	for _, b := range s.BranchList[repoID] {
		if b.Name == name {
			return s.annotatedBranch(repoID, b), nil
		}
	}
	return code.Branch{}, repos.ErrNotFound
}

// annotatedBranch is b with its commit's annotations.
func (s *Store) annotatedBranch(repoID uuid.UUID, b code.Branch) code.Branch {
	if b.Commit != nil {
		c := s.annotated(repoID, *b.Commit)
		b.Commit = &c
	}
	return b
}

// annotated is c with its annotations.
func (s *Store) annotated(repoID uuid.UUID, c code.Commit) code.Commit {
	c.Annotations = slices.Clone(s.Notes[repoID][c.SHA])
	return c
}

// FirstParents walks the repository's commits from sha along first parents.
func (s *Store) FirstParents(_ context.Context, repoID uuid.UUID, sha string, limit int) ([]code.Commit, error) {
	if s.Fail != nil {
		return nil, s.Fail
	}
	var out []code.Commit
	for c, ok := s.Commits[repoID][sha]; ok && len(out) < limit; c, ok = s.Commits[repoID][firstParent(c)] {
		out = append(out, s.annotated(repoID, c))
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
	return s.annotated(repoID, c), s.Pushes[c.FirstPush], nil
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

// Annotations lists the annotations on the repository's commit by name, or repos.ErrNotFound without the commit.
func (s *Store) Annotations(_ context.Context, repoID uuid.UUID, sha string) ([]code.Annotation, error) {
	if s.Fail != nil {
		return nil, s.Fail
	}
	if _, ok := s.Commits[repoID][sha]; !ok {
		return nil, repos.ErrNotFound
	}
	return slices.Clone(s.Notes[repoID][sha]), nil
}

// SetAnnotation sets the annotation on the repository's commit for actor, as code.Store does.
func (s *Store) SetAnnotation(_ context.Context, actor uuid.UUID, repoID uuid.UUID, sha string, in code.AnnotationInput) (code.Annotation, error) {
	if s.Fail != nil {
		return code.Annotation{}, s.Fail
	}
	if _, ok := s.Commits[repoID][sha]; !ok {
		return code.Annotation{}, repos.ErrNotFound
	}
	if s.Notes[repoID] == nil {
		s.Notes[repoID] = map[string][]code.Annotation{}
	}
	list := s.Notes[repoID][sha]
	i := slices.IndexFunc(list, func(a code.Annotation) bool { return a.Name == in.Name })
	switch {
	case i < 0 && in.Version != 0:
		return code.Annotation{}, code.ErrAnnotationChanged
	case i < 0:
		return s.insertAnnotation(actor, repoID, sha, in), nil
	}
	a := &list[i]
	switch {
	case a.Principal == nil || a.Principal.ID != actor:
		return code.Annotation{}, &code.TakenError{Name: in.Name, By: a.Principal}
	case code.Stale(in.Version, a.Version):
		return code.Annotation{}, code.ErrAnnotationChanged
	case a.Status == in.Status && a.Summary == in.Summary && a.Link == in.Link:
		return *a, nil
	}
	if a.Status.Done() && !in.Status.Done() {
		a.StartedAt = s.Now
	}
	switch {
	case !in.Status.Done():
		a.CompletedAt = nil
	case a.Status != in.Status:
		at := s.Now
		a.CompletedAt = &at
	}
	a.Status, a.Summary, a.Link, a.UpdatedAt = in.Status, in.Summary, in.Link, s.Now
	a.Version++
	return *a, nil
}

// insertAnnotation sets the annotation, at version 1, that the repository's commit doesn't hold yet.
func (s *Store) insertAnnotation(actor uuid.UUID, repoID uuid.UUID, sha string, in code.AnnotationInput) code.Annotation {
	a := code.Annotation{ID: uuid.New(), Name: in.Name, Status: in.Status, Summary: in.Summary, Link: in.Link,
		Principal: &code.Principal{ID: actor, DisplayName: s.Names[actor]}, CreatedAt: s.Now, UpdatedAt: s.Now, StartedAt: s.Now, Version: 1}
	if in.Status.Done() {
		at := s.Now
		a.CompletedAt = &at
	}
	list := append(s.Notes[repoID][sha], a)
	slices.SortFunc(list, func(a, b code.Annotation) int { return strings.Compare(a.Name, b.Name) })
	s.Notes[repoID][sha] = list
	return a
}

// DeleteAnnotation deletes the annotation from the repository's commit, as code.Store does.
func (s *Store) DeleteAnnotation(_ context.Context, actor uuid.UUID, repoID uuid.UUID, sha, name string, version int64, anyone bool) error {
	if s.Fail != nil {
		return s.Fail
	}
	list := s.Notes[repoID][sha]
	i := slices.IndexFunc(list, func(a code.Annotation) bool { return a.Name == name })
	if i < 0 {
		return repos.ErrNotFound
	}
	if a := list[i]; !anyone && (a.Principal == nil || a.Principal.ID != actor) {
		return &code.TakenError{Name: name, By: a.Principal}
	} else if code.Stale(version, a.Version) {
		return code.ErrAnnotationChanged
	}
	s.Notes[repoID][sha] = slices.Delete(list, i, i+1)
	return nil
}
