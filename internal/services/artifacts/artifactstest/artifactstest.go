// Package artifactstest holds a fake of the artifacts domain's port, for the tests of its service and of the adapters
// that serve it: a Store in memory over an orgstest.Store, which keeps the grants the authorizer reads.
package artifactstest

import (
	"bytes"
	"context"
	"slices"
	"time"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/artifacts"
	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs/orgstest"
)

// Task is an agent's task as an artifact names it: its number and its branch.
type Task struct {
	Number int64
	Branch string
}

// Store is an artifacts.Store in memory. Its grants are Orgs', the grants on people too, so one authorizer over Orgs
// reads both; the repositories it attaches artifacts to are those Orgs maps to their organizations. Fail, when set,
// fails every write.
type Store struct {
	Orgs      *orgstest.Store
	Artifacts map[uuid.UUID]artifacts.Artifact
	// Kept holds each artifact's versions, oldest first, with their bodies.
	Kept map[uuid.UUID][]artifacts.Version
	// RepoSlugs names the repositories, and Tasks the agents' tasks artifacts are written in.
	RepoSlugs map[uuid.UUID]string
	Tasks     map[uuid.UUID]Task
	// Others are principals of an organization besides its members, by ID: its API keys, jobs and agents. Colleague
	// finds its agents.
	Others map[uuid.UUID]Other
	Events []string
	Fail   error
	// Now is the time the store writes.
	Now time.Time
}

// Other is a principal of OrgID besides its members.
type Other struct {
	OrgID     uuid.UUID
	Principal artifacts.Principal
}

var _ artifacts.Store = (*Store)(nil)

// NewStore returns an empty Store over orgs.
func NewStore(o *orgstest.Store) *Store {
	return &Store{Orgs: o, Artifacts: map[uuid.UUID]artifacts.Artifact{}, Kept: map[uuid.UUID][]artifacts.Version{},
		RepoSlugs: map[uuid.UUID]string{}, Tasks: map[uuid.UUID]Task{}, Others: map[uuid.UUID]Other{},
		Now: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)}
}

// named is the principal id with its display name, as Orgs or Others know it.
func (s *Store) named(id uuid.UUID) artifacts.Principal {
	if id == uuid.Nil {
		return artifacts.Principal{}
	}
	if o, ok := s.Others[id]; ok {
		return o.Principal
	}
	return artifacts.Principal{ID: id, Kind: auth.KindUser, DisplayName: s.Orgs.Names[id]}
}

// sync tells Orgs where a lives, whose it is and how far its audiences reach it, which the authorizer reads.
func (s *Store) sync(a artifacts.Artifact) {
	s.Orgs.Artifacts[a.ID] = orgstest.ArtifactScope{OrgID: a.OrgID, RepoID: a.RepoID, OwnerID: a.Owner.ID,
		RepositoryAccess: string(a.RepositoryAccess), OrganizationAccess: string(a.OrganizationAccess)}
}

// With lists the IDs of the organization's artifacts, or the repository's for a repoID, where the principal holds
// permission, by ID.
func (s *Store) With(ctx context.Context, principalID, orgID, repoID uuid.UUID, permission string) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for id, a := range s.Artifacts {
		if a.OrgID != orgID || (repoID != uuid.Nil && a.RepoID != repoID) {
			continue
		}
		if held, _ := s.Orgs.Permissions(ctx, principalID, auth.ScopeArtifact, id); slices.Contains(held, permission) {
			out = append(out, id)
		}
	}
	slices.SortFunc(out, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return out, nil
}

// List reads the artifacts ids names, newest change first.
func (s *Store) List(_ context.Context, ids []uuid.UUID) ([]artifacts.Artifact, error) {
	var out []artifacts.Artifact
	for _, id := range ids {
		if a, ok := s.Artifacts[id]; ok {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b artifacts.Artifact) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}
		return bytes.Compare(b.ID[:], a.ID[:])
	})
	return out, nil
}

// Get finds an artifact.
func (s *Store) Get(_ context.Context, id uuid.UUID) (artifacts.Artifact, error) {
	a, ok := s.Artifacts[id]
	if !ok {
		return artifacts.Artifact{}, artifacts.ErrNotFound
	}
	return a, nil
}

// versionTaken reports whether a version has the ID.
func (s *Store) versionTaken(id uuid.UUID) bool {
	for _, list := range s.Kept {
		for _, v := range list {
			if v.ID == id {
				return true
			}
		}
	}
	return false
}

// Create writes the artifact, its first version and its makers' roles, with its task's branch when it names none.
func (s *Store) Create(_ context.Context, actor uuid.UUID, n artifacts.New) (artifacts.Artifact, error) {
	if s.Fail != nil {
		return artifacts.Artifact{}, s.Fail
	}
	a := n.Artifact
	if a.RepoID != uuid.Nil && s.Orgs.Repos[a.RepoID] != a.OrgID {
		return artifacts.Artifact{}, artifacts.ErrNotFound
	}
	if _, ok := s.Artifacts[a.ID]; ok || s.versionTaken(n.First.ID) {
		return artifacts.Artifact{}, artifacts.ErrExists
	}
	task := s.Tasks[a.TaskID]
	if a.Branch == "" {
		a.Branch = task.Branch
	}
	a.OrgSlug, a.RepoSlug, a.Head, a.MediaType, a.Version = s.Orgs.Orgs[a.OrgID].Slug, s.RepoSlugs[a.RepoID], 1, n.First.MediaType, 1
	a.Owner, a.Agent, a.TaskNumber = s.named(a.Owner.ID), s.named(a.Agent.ID), task.Number
	a.CreatedBy, a.UpdatedBy, a.CreatedAt, a.UpdatedAt = s.named(actor), s.named(actor), s.Now, s.Now
	a.Permissions = nil
	s.Artifacts[a.ID] = a
	s.Kept[a.ID] = []artifacts.Version{s.version(a.ID, 1, actor, a.TaskID, n.First)}
	for _, g := range n.Owners {
		s.Orgs.Grant(g.PrincipalID, g.RoleID, auth.ScopeArtifact, a.ID)
	}
	s.sync(a)
	s.Events = append(s.Events, "artifact.created")
	return a, nil
}

// version is the version number of id that actor writes from in, in taskID.
func (s *Store) version(id uuid.UUID, number int32, actor, taskID uuid.UUID, in artifacts.VersionInput) artifacts.Version {
	return artifacts.Version{ID: in.ID, ArtifactID: id, Number: number, MediaType: in.MediaType, Size: int64(len(in.Body)), Author: s.named(actor),
		TaskID: taskID, TaskNumber: s.Tasks[taskID].Number, Note: in.Note, CreatedAt: s.Now, Body: in.Body}
}

// at finds the artifact and checks it is at version (any with AnyVersion).
func (s *Store) at(id uuid.UUID, version int64) (artifacts.Artifact, error) {
	if s.Fail != nil {
		return artifacts.Artifact{}, s.Fail
	}
	a, ok := s.Artifacts[id]
	switch {
	case !ok:
		return artifacts.Artifact{}, artifacts.ErrNotFound
	case version != artifacts.AnyVersion && a.Version != version:
		return artifacts.Artifact{}, artifacts.ErrChanged
	}
	return a, nil
}

// Update changes the artifact's row if it is still at version.
func (s *Store) Update(_ context.Context, actor uuid.UUID, id uuid.UUID, version int64, c artifacts.Changes) (artifacts.Artifact, error) {
	a, err := s.at(id, version)
	if err != nil {
		return artifacts.Artifact{}, err
	}
	a.Title, a.Summary, a.RepositoryAccess, a.OrganizationAccess = c.Title, c.Summary, c.RepositoryAccess, c.OrganizationAccess
	a.Version, a.UpdatedBy, a.UpdatedAt = a.Version+1, s.named(actor), s.Now
	s.Artifacts[id] = a
	s.sync(a)
	s.Events = append(s.Events, "artifact.changed")
	return a, nil
}

// Delete deletes the artifact, its versions and the grants on it if it is still at version.
func (s *Store) Delete(_ context.Context, _ uuid.UUID, id uuid.UUID, version int64) error {
	if _, err := s.at(id, version); err != nil {
		return err
	}
	delete(s.Artifacts, id)
	delete(s.Kept, id)
	delete(s.Orgs.Artifacts, id)
	s.Orgs.Grants = slices.DeleteFunc(s.Orgs.Grants, func(g orgstest.Grant) bool { return g.Scope == auth.ScopeArtifact && g.ScopeID == id })
	s.Events = append(s.Events, "artifact.deleted")
	return nil
}

// AddVersion adds the artifact's next version if it is still at version.
func (s *Store) AddVersion(_ context.Context, actor uuid.UUID, id uuid.UUID, version int64, in artifacts.VersionInput, taskID uuid.UUID) (artifacts.Artifact, error) {
	a, err := s.at(id, version)
	if err != nil {
		return artifacts.Artifact{}, err
	}
	if s.versionTaken(in.ID) {
		return artifacts.Artifact{}, artifacts.ErrExists
	}
	a.Head++
	s.Kept[id] = append(s.Kept[id], s.version(id, a.Head, actor, taskID, in))
	a.MediaType, a.Version, a.UpdatedBy, a.UpdatedAt = in.MediaType, a.Version+1, s.named(actor), s.Now
	s.Artifacts[id] = a
	s.Events = append(s.Events, "artifact.version_added")
	return a, nil
}

// Versions lists the artifact's versions, newest first, without their bodies.
func (s *Store) Versions(_ context.Context, id uuid.UUID) ([]artifacts.Version, error) {
	list := slices.Clone(s.Kept[id])
	slices.Reverse(list)
	for i := range list {
		list[i].Body = ""
	}
	return list, nil
}

// Version finds a version of the artifact with its body.
func (s *Store) Version(_ context.Context, id uuid.UUID, number int32) (artifacts.Version, error) {
	for _, v := range s.Kept[id] {
		if v.Number == number {
			return v, nil
		}
	}
	return artifacts.Version{}, artifacts.ErrNotFound
}

// VersionByID finds the version a call wrote, by its ID, with its body.
func (s *Store) VersionByID(_ context.Context, versionID uuid.UUID) (artifacts.Version, error) {
	for _, list := range s.Kept {
		for _, v := range list {
			if v.ID == versionID {
				return v, nil
			}
		}
	}
	return artifacts.Version{}, artifacts.ErrNotFound
}

// Grants lists the roles principals hold on the artifact itself, by display name.
func (s *Store) Grants(_ context.Context, id uuid.UUID) ([]artifacts.Grant, error) {
	return s.grants(auth.ScopeArtifact, id), nil
}

// PersonGrants lists the roles principals hold on the user, by display name.
func (s *Store) PersonGrants(_ context.Context, userID uuid.UUID) ([]artifacts.Grant, error) {
	return s.grants(auth.ScopeUser, userID), nil
}

// grants lists the artifact roles principals hold at the scope, by display name.
func (s *Store) grants(scope auth.Scope, id uuid.UUID) []artifacts.Grant {
	out := []artifacts.Grant{}
	for _, g := range s.Orgs.Grants {
		if g.Scope != scope || g.ScopeID != id {
			continue
		}
		if role, ok := artifacts.RoleOf(g.Role); ok {
			out = append(out, artifacts.Grant{Principal: s.named(g.Principal), Role: role})
		}
	}
	slices.SortFunc(out, func(a, b artifacts.Grant) int {
		if a.Principal.DisplayName != b.Principal.DisplayName {
			if a.Principal.DisplayName < b.Principal.DisplayName {
				return -1
			}
			return 1
		}
		return bytes.Compare(a.Principal.ID[:], b.Principal.ID[:])
	})
	return out
}

// Principal finds one of the organization's members, or one of its own principals.
func (s *Store) Principal(_ context.Context, orgID, principalID uuid.UUID) (artifacts.Principal, error) {
	if o, ok := s.Others[principalID]; ok && o.OrgID == orgID {
		return o.Principal, nil
	}
	if slices.Contains(s.Orgs.Memberships[orgID], principalID) {
		return s.named(principalID), nil
	}
	return artifacts.Principal{}, artifacts.ErrNotFound
}

// Colleague finds a member of one of the user's organizations, or an agent of one.
func (s *Store) Colleague(_ context.Context, userID, principalID uuid.UUID) (artifacts.Principal, error) {
	for org, members := range s.Orgs.Memberships {
		if principalID == userID || !slices.Contains(members, userID) {
			continue
		}
		if o, ok := s.Others[principalID]; ok && o.OrgID == org && o.Principal.Kind == auth.KindAgent {
			return o.Principal, nil
		}
		if slices.Contains(members, principalID) {
			return s.named(principalID), nil
		}
	}
	return artifacts.Principal{}, artifacts.ErrNotFound
}

// dropGrants takes every role principalID holds at the scope, and answers how many it took.
func (s *Store) dropGrants(scope auth.Scope, id, principalID uuid.UUID) int {
	before := len(s.Orgs.Grants)
	s.Orgs.Grants = slices.DeleteFunc(s.Orgs.Grants, func(g orgstest.Grant) bool {
		return g.Principal == principalID && g.Scope == scope && g.ScopeID == id
	})
	return before - len(s.Orgs.Grants)
}

// Share gives the principal the role on the artifact in place of any artifact role it held there.
func (s *Store) Share(_ context.Context, _ uuid.UUID, a artifacts.Artifact, principalID, roleID uuid.UUID) error {
	if _, err := s.at(a.ID, artifacts.AnyVersion); err != nil {
		return err
	}
	s.dropGrants(auth.ScopeArtifact, a.ID, principalID)
	s.Orgs.Grant(principalID, roleID, auth.ScopeArtifact, a.ID)
	s.Events = append(s.Events, "artifact.shared")
	return nil
}

// Unshare takes every role the principal holds on the artifact itself.
func (s *Store) Unshare(_ context.Context, _ uuid.UUID, a artifacts.Artifact, principalID uuid.UUID) error {
	if s.Fail != nil {
		return s.Fail
	}
	if s.dropGrants(auth.ScopeArtifact, a.ID, principalID) == 0 {
		return artifacts.ErrNotFound
	}
	s.Events = append(s.Events, "artifact.unshared")
	return nil
}

// SharePerson gives the principal the role on actor in place of any role it held there.
func (s *Store) SharePerson(_ context.Context, actor, principalID, roleID uuid.UUID) error {
	if s.Fail != nil {
		return s.Fail
	}
	s.dropGrants(auth.ScopeUser, actor, principalID)
	s.Orgs.Grant(principalID, roleID, auth.ScopeUser, actor)
	return nil
}

// UnsharePerson takes every role the principal holds on actor.
func (s *Store) UnsharePerson(_ context.Context, actor, principalID uuid.UUID) error {
	if s.Fail != nil {
		return s.Fail
	}
	if s.dropGrants(auth.ScopeUser, actor, principalID) == 0 {
		return artifacts.ErrNotFound
	}
	return nil
}
