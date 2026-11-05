package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/artifacts"
	"cobranch.dev/internal/services/auth"
)

// ArtifactStore keeps the organizations' artifacts, their versions and the grants on them and on their owners
// (db/migrations/00049_artifacts.sql, 00055_artifact_owners.sql): artifacts.Store on PostgreSQL.
type ArtifactStore struct {
	pool *pgxpool.Pool
	q    *queries.Queries
}

// NewArtifactStore returns artifacts' store on pool's database.
func NewArtifactStore(pool *pgxpool.Pool) *ArtifactStore {
	return &ArtifactStore{pool: pool, q: queries.New(pool)}
}

var _ artifacts.Store = (*ArtifactStore)(nil)

// transact runs fn in a transaction that acts for actor (transact), mapping a taken ID to artifacts.ErrExists.
func (s *ArtifactStore) transact(ctx context.Context, what string, actor uuid.UUID, fn func(ctx context.Context, q *queries.Queries) error) error {
	err := transact(ctx, s.pool, s.q, what, actor, fn)
	var conflict *ConflictError
	if errors.As(err, &conflict) && (conflict.Constraint == "artifacts_pkey" || conflict.Constraint == "artifact_versions_id_key") {
		return artifacts.ErrExists
	}
	return err
}

// wrapArtifact returns artifacts.ErrNotFound for a query that found no row, and err in what's context otherwise.
func wrapArtifact(what string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return artifacts.ErrNotFound
	}
	return fmt.Errorf("%s: %w", what, err)
}

// artifactPrincipal is the principal id and name name, or none when the principal is gone.
func artifactPrincipal(id *uuid.UUID, name *string) artifacts.Principal {
	if id == nil {
		return artifacts.Principal{}
	}
	p := artifacts.Principal{ID: *id}
	if name != nil {
		p.DisplayName = *name
	}
	return p
}

// artifactOf is the domain's view of an artifacts row, which every query reading one answers in the same columns.
func artifactOf(r queries.ArtifactRow) artifacts.Artifact {
	a := artifacts.Artifact{ID: r.ID, OrgID: r.OrgID, OrgSlug: r.OrgSlug, Owner: artifactPrincipal(r.OwnerID, r.OwnerName), Title: r.Title,
		Summary: r.Summary, Kind: artifacts.Kind(r.Kind), RepositoryAccess: artifacts.Reach(r.RepositoryAccess),
		OrganizationAccess: artifacts.Reach(r.OrganizationAccess), Agent: artifactPrincipal(r.AgentID, r.AgentName), Branch: r.Branch,
		Head: r.Head, MediaType: r.MediaType, Version: r.Version, CreatedBy: artifactPrincipal(r.CreatedBy, r.CreatedByName),
		CreatedAt: r.CreatedAt, UpdatedBy: artifactPrincipal(r.UpdatedBy, r.UpdatedByName), UpdatedAt: r.UpdatedAt}
	if r.RepoID != nil && r.RepoSlug != nil {
		a.RepoID, a.RepoSlug = *r.RepoID, *r.RepoSlug
	}
	if r.TaskID != nil {
		a.TaskID = *r.TaskID
	}
	if r.TaskNumber != nil {
		a.TaskNumber = *r.TaskNumber
	}
	return a
}

// versionOf is the domain's view of an artifact_versions row, with its body.
func versionOf(r queries.ArtifactVersionRow) artifacts.Version {
	v := artifacts.Version{ID: r.ID, ArtifactID: r.ArtifactID, Number: r.Number, MediaType: r.MediaType, Size: int64(len(r.Body)),
		Author: artifactPrincipal(r.AuthorID, r.AuthorName), Note: r.Note, CreatedAt: r.CreatedAt, Body: r.Body}
	if r.TaskID != nil {
		v.TaskID = *r.TaskID
	}
	if r.TaskNumber != nil {
		v.TaskNumber = *r.TaskNumber
	}
	return v
}

// With lists the IDs of the organization's artifacts, or the repository's for a repoID, where the principal holds
// permission.
func (s *ArtifactStore) With(ctx context.Context, principalID, orgID, repoID uuid.UUID, permission string) ([]uuid.UUID, error) {
	ids, err := s.q.ArtifactsWith(ctx, queries.ArtifactsWithParams{OrgID: orgID, RepoID: nullable(repoID), PrincipalID: principalID,
		Permission: permission})
	if err != nil {
		return nil, fmt.Errorf("listing the artifacts with %s: %w", permission, err)
	}
	return ids, nil
}

// List reads the artifacts ids names, newest change first.
func (s *ArtifactStore) List(ctx context.Context, ids []uuid.UUID) ([]artifacts.Artifact, error) {
	rows, err := s.q.Artifacts(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("reading the artifacts: %w", err)
	}
	out := make([]artifacts.Artifact, 0, len(rows))
	for _, r := range rows {
		out = append(out, artifactOf(queries.ArtifactRow(r)))
	}
	return out, nil
}

// Get finds an artifact.
func (s *ArtifactStore) Get(ctx context.Context, id uuid.UUID) (artifacts.Artifact, error) {
	return get(ctx, s.q, id)
}

func get(ctx context.Context, q *queries.Queries, id uuid.UUID) (artifacts.Artifact, error) {
	r, err := q.Artifact(ctx, id)
	if err != nil {
		return artifacts.Artifact{}, wrapArtifact("reading the artifact", err)
	}
	return artifactOf(r), nil
}

// Create writes the artifact, its first version and its makers' roles, and records the event.
func (s *ArtifactStore) Create(ctx context.Context, actor uuid.UUID, n artifacts.New) (artifacts.Artifact, error) {
	var out artifacts.Artifact
	a := n.Artifact
	err := s.transact(ctx, "creating the artifact", actor, func(ctx context.Context, q *queries.Queries) error {
		inserted, err := q.InsertArtifact(ctx, queries.InsertArtifactParams{ID: a.ID, OrgID: a.OrgID, RepoID: nullable(a.RepoID),
			OwnerID: nullable(a.Owner.ID), Title: a.Title,
			Summary: a.Summary, Kind: string(a.Kind), RepositoryAccess: string(a.RepositoryAccess), OrganizationAccess: string(a.OrganizationAccess),
			AgentID: nullable(a.Agent.ID), TaskID: nullable(a.TaskID), Branch: a.Branch, Actor: actor})
		if err != nil {
			return fmt.Errorf("inserting the artifact: %w", err)
		}
		if inserted == 0 {
			return artifacts.ErrNotFound
		}
		if err := q.InsertArtifactVersion(ctx, queries.InsertArtifactVersionParams{ArtifactID: a.ID, Number: 1, ID: n.First.ID,
			MediaType: n.First.MediaType, Body: n.First.Body, AuthorID: actor, TaskID: nullable(a.TaskID), Note: n.First.Note}); err != nil {
			return fmt.Errorf("inserting the first version: %w", err)
		}
		for _, g := range n.Owners {
			if _, err := q.InsertGrant(ctx, queries.InsertGrantParams{PrincipalID: g.PrincipalID, RoleID: g.RoleID,
				Scope: string(auth.ScopeArtifact), ScopeID: &a.ID, Source: string(auth.SourceGrant)}); err != nil {
				return fmt.Errorf("granting the artifact's makers: %w", err)
			}
		}
		if out, err = get(ctx, q, a.ID); err != nil {
			return err
		}
		return orgEvent(ctx, q, &a.OrgID, actor, "artifact.created", a.ID, map[string]any{"title": a.Title, "repo": nullable(a.RepoID),
			"owner": nullable(a.Owner.ID), "kind": a.Kind, "task": nullable(a.TaskID)})
	})
	return out, err
}

// lockArtifact locks the artifact and checks it is at version (any with AnyVersion).
func lockArtifact(ctx context.Context, q *queries.Queries, id uuid.UUID, version int64) (queries.ArtifactForUpdateRow, error) {
	r, err := q.ArtifactForUpdate(ctx, id)
	if err != nil {
		return r, wrapArtifact("locking the artifact", err)
	}
	if version != artifacts.AnyVersion && r.Version != version {
		return r, artifacts.ErrChanged
	}
	return r, nil
}

// Update changes the artifact's row if it is still at version, and records the event.
func (s *ArtifactStore) Update(ctx context.Context, actor uuid.UUID, id uuid.UUID, version int64, c artifacts.Changes) (artifacts.Artifact, error) {
	var out artifacts.Artifact
	err := s.transact(ctx, "changing the artifact", actor, func(ctx context.Context, q *queries.Queries) error {
		before, err := lockArtifact(ctx, q, id, version)
		if err != nil {
			return err
		}
		if err := q.UpdateArtifact(ctx, queries.UpdateArtifactParams{Title: c.Title, Summary: c.Summary, RepositoryAccess: string(c.RepositoryAccess),
			OrganizationAccess: string(c.OrganizationAccess), Actor: actor, ID: id}); err != nil {
			return fmt.Errorf("updating the artifact: %w", err)
		}
		if out, err = get(ctx, q, id); err != nil {
			return err
		}
		return orgEvent(ctx, q, &before.OrgID, actor, "artifact.changed", id, map[string]any{"title": c.Title,
			"repositoryAccess": c.RepositoryAccess, "organizationAccess": c.OrganizationAccess})
	})
	return out, err
}

// Delete deletes the artifact, its versions and the grants on it if it is still at version, and records the event.
func (s *ArtifactStore) Delete(ctx context.Context, actor uuid.UUID, id uuid.UUID, version int64) error {
	return s.transact(ctx, "deleting the artifact", actor, func(ctx context.Context, q *queries.Queries) error {
		before, err := lockArtifact(ctx, q, id, version)
		if err != nil {
			return err
		}
		if err := q.DeleteArtifact(ctx, id); err != nil {
			return fmt.Errorf("deleting the artifact: %w", err)
		}
		return orgEvent(ctx, q, &before.OrgID, actor, "artifact.deleted", id, map[string]any{"title": before.Title, "repo": before.RepoID})
	})
}

// AddVersion adds the artifact's next version if it is still at version, and records the event.
func (s *ArtifactStore) AddVersion(ctx context.Context, actor uuid.UUID, id uuid.UUID, version int64, in artifacts.VersionInput, taskID uuid.UUID) (artifacts.Artifact, error) {
	var out artifacts.Artifact
	err := s.transact(ctx, "adding the artifact's version", actor, func(ctx context.Context, q *queries.Queries) error {
		before, err := lockArtifact(ctx, q, id, version)
		if err != nil {
			return err
		}
		number := before.Head + 1
		if err := q.InsertArtifactVersion(ctx, queries.InsertArtifactVersionParams{ArtifactID: id, Number: number, ID: in.ID,
			MediaType: in.MediaType, Body: in.Body, AuthorID: actor, TaskID: nullable(taskID), Note: in.Note}); err != nil {
			return fmt.Errorf("inserting the version: %w", err)
		}
		if err := q.AdvanceArtifact(ctx, number, actor, id); err != nil {
			return fmt.Errorf("advancing the artifact: %w", err)
		}
		if out, err = get(ctx, q, id); err != nil {
			return err
		}
		return orgEvent(ctx, q, &before.OrgID, actor, "artifact.version_added", id, map[string]any{"number": number,
			"mediaType": in.MediaType, "task": nullable(taskID)})
	})
	return out, err
}

// Versions lists the artifact's versions, newest first, without their bodies.
func (s *ArtifactStore) Versions(ctx context.Context, id uuid.UUID) ([]artifacts.Version, error) {
	rows, err := s.q.ArtifactVersions(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("listing the artifact's versions: %w", err)
	}
	out := make([]artifacts.Version, 0, len(rows))
	for _, r := range rows {
		v := versionOf(queries.ArtifactVersionRow{ArtifactID: r.ArtifactID, Number: r.Number, ID: r.ID, MediaType: r.MediaType,
			AuthorID: r.AuthorID, AuthorName: r.AuthorName, TaskID: r.TaskID, TaskNumber: r.TaskNumber, Note: r.Note, CreatedAt: r.CreatedAt})
		v.Size = r.Size
		out = append(out, v)
	}
	return out, nil
}

// Version finds a version of the artifact with its body.
func (s *ArtifactStore) Version(ctx context.Context, id uuid.UUID, number int32) (artifacts.Version, error) {
	r, err := s.q.ArtifactVersion(ctx, id, number)
	if err != nil {
		return artifacts.Version{}, wrapArtifact("reading the version", err)
	}
	return versionOf(r), nil
}

// VersionByID finds the version a call wrote, by its ID, with its body.
func (s *ArtifactStore) VersionByID(ctx context.Context, versionID uuid.UUID) (artifacts.Version, error) {
	r, err := s.q.ArtifactVersionByID(ctx, versionID)
	if err != nil {
		return artifacts.Version{}, wrapArtifact("finding the version", err)
	}
	return s.Version(ctx, r.ArtifactID, r.Number)
}

// Grants lists the roles principals hold on the artifact itself, by display name.
func (s *ArtifactStore) Grants(ctx context.Context, id uuid.UUID) ([]artifacts.Grant, error) {
	rows, err := s.q.ArtifactGrantees(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("listing the artifact's grants: %w", err)
	}
	return artifactGrants(rows), nil
}

// PersonGrants lists the roles principals hold on the user, by display name.
func (s *ArtifactStore) PersonGrants(ctx context.Context, userID uuid.UUID) ([]artifacts.Grant, error) {
	rows, err := s.q.PersonGrantees(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing the grants on the person: %w", err)
	}
	grantees := make([]queries.ArtifactGranteesRow, 0, len(rows))
	for _, r := range rows {
		grantees = append(grantees, queries.ArtifactGranteesRow(r))
	}
	return artifactGrants(grantees), nil
}

// artifactGrants is the domain's view of grantees' rows, leaving out a role that is none of the artifact roles.
func artifactGrants(rows []queries.ArtifactGranteesRow) []artifacts.Grant {
	out := make([]artifacts.Grant, 0, len(rows))
	for _, r := range rows {
		if role, ok := artifacts.RoleOf(r.RoleID); ok {
			out = append(out, artifacts.Grant{Principal: artifacts.Principal{ID: r.ID, Kind: auth.Kind(r.Kind), DisplayName: r.DisplayName}, Role: role})
		}
	}
	return out
}

// Principal finds one of the organization's members, or a principal of its own: an API key, a job or an agent.
func (s *ArtifactStore) Principal(ctx context.Context, orgID, principalID uuid.UUID) (artifacts.Principal, error) {
	r, err := s.q.Grantee(ctx, principalID, orgID)
	if err != nil {
		return artifacts.Principal{}, wrapArtifact("finding the principal", err)
	}
	return artifacts.Principal{ID: r.ID, Kind: auth.Kind(r.Kind), DisplayName: r.DisplayName}, nil
}

// Colleague finds a member of one of the user's organizations, or an agent of one.
func (s *ArtifactStore) Colleague(ctx context.Context, userID, principalID uuid.UUID) (artifacts.Principal, error) {
	r, err := s.q.Colleague(ctx, principalID, userID)
	if err != nil {
		return artifacts.Principal{}, wrapArtifact("finding the principal", err)
	}
	return artifacts.Principal{ID: r.ID, Kind: auth.Kind(r.Kind), DisplayName: r.DisplayName}, nil
}

// Share gives the principal the role on the artifact in place of any artifact role it held there, and records it.
func (s *ArtifactStore) Share(ctx context.Context, actor uuid.UUID, a artifacts.Artifact, principalID, roleID uuid.UUID) error {
	return s.transact(ctx, "sharing the artifact", actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.LockOrganization(ctx, a.OrgID); err != nil {
			return wrapArtifact("locking the organization", err)
		}
		if _, err := lockArtifact(ctx, q, a.ID, artifacts.AnyVersion); err != nil {
			return err
		}
		if _, err := q.DeleteArtifactGrants(ctx, principalID, a.ID); err != nil {
			return fmt.Errorf("deleting the roles it held: %w", err)
		}
		if _, err := q.InsertGrant(ctx, queries.InsertGrantParams{PrincipalID: principalID, RoleID: roleID,
			Scope: string(auth.ScopeArtifact), ScopeID: &a.ID, Source: string(auth.SourceGrant)}); err != nil {
			return fmt.Errorf("inserting the grant: %w", err)
		}
		return orgEvent(ctx, q, &a.OrgID, actor, "artifact.shared", principalID, map[string]any{"artifact": a.ID, "role": roleID})
	})
}

// Unshare takes every role the principal holds on the artifact itself, and records it.
func (s *ArtifactStore) Unshare(ctx context.Context, actor uuid.UUID, a artifacts.Artifact, principalID uuid.UUID) error {
	return s.transact(ctx, "unsharing the artifact", actor, func(ctx context.Context, q *queries.Queries) error {
		n, err := q.DeleteArtifactGrants(ctx, principalID, a.ID)
		if err != nil {
			return fmt.Errorf("deleting the grants: %w", err)
		}
		if n == 0 {
			return artifacts.ErrNotFound
		}
		return orgEvent(ctx, q, &a.OrgID, actor, "artifact.unshared", principalID, map[string]any{"artifact": a.ID})
	})
}

// SharePerson gives the principal the role on actor in place of any role it held there, and records it as an event of
// the installation's, since a person's artifacts span their organizations.
func (s *ArtifactStore) SharePerson(ctx context.Context, actor, principalID, roleID uuid.UUID) error {
	return s.transact(ctx, "sharing the person's artifacts", actor, func(ctx context.Context, q *queries.Queries) error {
		if _, err := q.DeletePersonGrants(ctx, principalID, actor); err != nil {
			return fmt.Errorf("deleting the roles it held: %w", err)
		}
		if _, err := q.InsertGrant(ctx, queries.InsertGrantParams{PrincipalID: principalID, RoleID: roleID,
			Scope: string(auth.ScopeUser), ScopeID: &actor, Source: string(auth.SourceGrant)}); err != nil {
			return fmt.Errorf("inserting the grant: %w", err)
		}
		return orgEvent(ctx, q, nil, actor, "artifacts.person_shared", principalID, map[string]any{"role": roleID})
	})
}

// UnsharePerson takes every role the principal holds on actor, and records it as SharePerson does.
func (s *ArtifactStore) UnsharePerson(ctx context.Context, actor, principalID uuid.UUID) error {
	return s.transact(ctx, "unsharing the person's artifacts", actor, func(ctx context.Context, q *queries.Queries) error {
		n, err := q.DeletePersonGrants(ctx, principalID, actor)
		if err != nil {
			return fmt.Errorf("deleting the grants: %w", err)
		}
		if n == 0 {
			return artifacts.ErrNotFound
		}
		return orgEvent(ctx, q, nil, actor, "artifacts.person_unshared", principalID, nil)
	})
}
