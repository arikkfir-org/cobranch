-- ArtifactPermissions lists the permissions the principal holds on an artifact, as artifact_permissions answers them
-- (db/migrations/00055_artifact_owners.sql), kept to the artifact permissions, which permissions lists.
-- name: ArtifactPermissions :many
SELECT DISTINCT ap.permission::text AS permission FROM artifact_permissions(@principal_id::uuid, @artifact_id::uuid) ap
WHERE ap.permission = ANY (@permissions::text[])
ORDER BY 1;

-- ArtifactsWith lists the IDs of the organization's artifacts, or those attached to the repository when repo_id names
-- one, where the principal holds the permission, as ArtifactPermissions answers it.
-- name: ArtifactsWith :many
SELECT a.id FROM artifacts a
WHERE a.org_id = @org_id::uuid AND (sqlc.narg(repo_id)::uuid IS NULL OR a.repo_id = sqlc.narg(repo_id)::uuid)
  AND EXISTS (SELECT 1 FROM artifact_permissions(@principal_id::uuid, a.id) ap WHERE ap.permission = @permission::text)
ORDER BY a.id;

-- name: Artifact :one
SELECT a.id, a.org_id, o.slug AS org_slug, a.repo_id, rp.slug AS repo_slug, a.owner_id, ow.display_name AS owner_name, a.title, a.summary, a.kind,
       a.repository_access, a.organization_access, a.agent_id, ag.display_name AS agent_name, a.task_id, t.number AS task_number, a.branch, a.head,
       v.media_type, a.version, a.created_by, cb.display_name AS created_by_name, a.created_at, a.updated_by, ub.display_name AS updated_by_name,
       a.updated_at
FROM artifacts a JOIN organizations o ON o.id = a.org_id LEFT JOIN repositories rp ON rp.id = a.repo_id
JOIN artifact_versions v ON v.artifact_id = a.id AND v.number = a.head
LEFT JOIN principals ow ON ow.id = a.owner_id
LEFT JOIN principals ag ON ag.id = a.agent_id
LEFT JOIN agent_tasks t ON t.id = a.task_id
LEFT JOIN principals cb ON cb.id = a.created_by
LEFT JOIN principals ub ON ub.id = a.updated_by
WHERE a.id = $1;

-- Artifacts reads the artifacts ids names, newest change first.
-- name: Artifacts :many
SELECT a.id, a.org_id, o.slug AS org_slug, a.repo_id, rp.slug AS repo_slug, a.owner_id, ow.display_name AS owner_name, a.title, a.summary, a.kind,
       a.repository_access, a.organization_access, a.agent_id, ag.display_name AS agent_name, a.task_id, t.number AS task_number, a.branch, a.head,
       v.media_type, a.version, a.created_by, cb.display_name AS created_by_name, a.created_at, a.updated_by, ub.display_name AS updated_by_name,
       a.updated_at
FROM artifacts a JOIN organizations o ON o.id = a.org_id LEFT JOIN repositories rp ON rp.id = a.repo_id
JOIN artifact_versions v ON v.artifact_id = a.id AND v.number = a.head
LEFT JOIN principals ow ON ow.id = a.owner_id
LEFT JOIN principals ag ON ag.id = a.agent_id
LEFT JOIN agent_tasks t ON t.id = a.task_id
LEFT JOIN principals cb ON cb.id = a.created_by
LEFT JOIN principals ub ON ub.id = a.updated_by
WHERE a.id = ANY (@ids::uuid[])
ORDER BY a.updated_at DESC, a.id DESC;

-- InsertArtifact writes an artifact of its organization, attached to one of its repositories or to none, with its
-- branch, or the branch of its task when it names none: no row when the repository is another organization's.
-- name: InsertArtifact :execrows
INSERT INTO artifacts (id, org_id, repo_id, owner_id, title, summary, kind, repository_access, organization_access, agent_id, task_id, branch,
                       created_by, updated_by)
SELECT @id, @org_id, sqlc.narg(repo_id)::uuid, sqlc.narg(owner_id)::uuid, @title, @summary, @kind, @repository_access, @organization_access,
       sqlc.narg(agent_id)::uuid, sqlc.narg(task_id)::uuid,
       coalesce(nullif(@branch::text, ''), (SELECT b.name FROM agent_tasks t JOIN branches b ON b.id = t.branch_id
                                            WHERE t.id = sqlc.narg(task_id)::uuid), ''),
       @actor::uuid, @actor::uuid
WHERE sqlc.narg(repo_id)::uuid IS NULL OR EXISTS (SELECT 1 FROM repositories WHERE id = sqlc.narg(repo_id)::uuid AND org_id = @org_id);

-- name: InsertArtifactVersion :exec
INSERT INTO artifact_versions (artifact_id, number, id, media_type, body, author_id, task_id, note)
VALUES (@artifact_id, @number, @id, @media_type, @body, @author_id::uuid, sqlc.narg(task_id)::uuid, @note);

-- ArtifactForUpdate locks an artifact to the end of the transaction.
-- name: ArtifactForUpdate :one
SELECT id, org_id, repo_id, title, head, version, created_by FROM artifacts WHERE id = $1 FOR UPDATE;

-- name: UpdateArtifact :exec
UPDATE artifacts SET title = @title, summary = @summary, repository_access = @repository_access,
    organization_access = @organization_access, updated_by = @actor::uuid, updated_at = now()
WHERE id = @id;

-- AdvanceArtifact makes number the artifact's newest version.
-- name: AdvanceArtifact :exec
UPDATE artifacts SET head = @number, updated_by = @actor::uuid, updated_at = now() WHERE id = @id;

-- DeleteArtifact deletes an artifact and its versions; trigger delete_grants takes the grants on it.
-- name: DeleteArtifact :exec
DELETE FROM artifacts WHERE id = $1;

-- ArtifactVersions lists an artifact's versions, newest first, without their bodies.
-- name: ArtifactVersions :many
SELECT v.artifact_id, v.number, v.id, v.media_type, octet_length(v.body)::bigint AS size, v.author_id, p.display_name AS author_name,
       v.task_id, t.number AS task_number, v.note, v.created_at
FROM artifact_versions v LEFT JOIN principals p ON p.id = v.author_id LEFT JOIN agent_tasks t ON t.id = v.task_id
WHERE v.artifact_id = $1 ORDER BY v.number DESC;

-- name: ArtifactVersion :one
SELECT v.artifact_id, v.number, v.id, v.media_type, v.body, v.author_id, p.display_name AS author_name,
       v.task_id, t.number AS task_number, v.note, v.created_at
FROM artifact_versions v LEFT JOIN principals p ON p.id = v.author_id LEFT JOIN agent_tasks t ON t.id = v.task_id
WHERE v.artifact_id = $1 AND v.number = $2;

-- ArtifactVersionByID finds the version a call wrote, by the call's ID.
-- name: ArtifactVersionByID :one
SELECT artifact_id, number, author_id FROM artifact_versions WHERE id = $1;

-- ArtifactGrantees lists the roles principals hold on an artifact itself, by display name.
-- name: ArtifactGrantees :many
SELECT p.id, p.kind, p.display_name, g.role_id FROM grants g JOIN principals p ON p.id = g.principal_id
WHERE g.scope = 'artifact' AND g.scope_id = @artifact_id::uuid ORDER BY p.display_name, p.id, g.role_id;

-- DeleteArtifactGrants takes every role the principal holds on the artifact.
-- name: DeleteArtifactGrants :execrows
DELETE FROM grants WHERE principal_id = @principal_id AND scope = 'artifact' AND scope_id = @artifact_id::uuid;

-- DeleteArtifactGrantsAt takes every role the principal holds on the organization's artifacts: they go with the
-- membership.
-- name: DeleteArtifactGrantsAt :execrows
DELETE FROM grants g USING artifacts a
WHERE g.principal_id = @principal_id AND g.scope = 'artifact' AND g.scope_id = a.id AND a.org_id = @org_id::uuid;

-- PersonGrantees lists the roles principals hold on the person, by display name: those that reach the artifacts the
-- person owns.
-- name: PersonGrantees :many
SELECT p.id, p.kind, p.display_name, g.role_id FROM grants g JOIN principals p ON p.id = g.principal_id
WHERE g.scope = 'user' AND g.scope_id = @user_id::uuid ORDER BY p.display_name, p.id, g.role_id;

-- DeletePersonGrants takes every role the principal holds on the person.
-- name: DeletePersonGrants :execrows
DELETE FROM grants WHERE principal_id = @principal_id AND scope = 'user' AND scope_id = @user_id::uuid;

-- Colleague finds a user who is a member of an organization the person is, or an agent of one: those the person may
-- grant a role on themselves.
-- name: Colleague :one
SELECT p.id, p.kind, p.display_name FROM principals p
WHERE p.id = @principal_id AND p.id <> @user_id::uuid AND (
    (p.kind = 'user' AND EXISTS (SELECT 1 FROM memberships m JOIN memberships theirs ON theirs.org_id = m.org_id
                                 WHERE m.user_id = @user_id::uuid AND theirs.user_id = p.id))
    OR (p.kind = 'agent' AND EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = @user_id::uuid AND m.org_id = p.org_id)));
