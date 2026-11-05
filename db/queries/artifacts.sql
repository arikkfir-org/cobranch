-- ArtifactPermissions lists the permissions the principal holds on an artifact: those of the roles its grants give
-- on the artifact, on its repository and at its organization, and those its audiences give a principal that reads its
-- repository, or its organization, view artifacts.read and edit artifacts.write besides; each kept to the artifact
-- permissions, which permissions lists.
-- name: ArtifactPermissions :many
SELECT DISTINCT p.permission::text AS permission FROM (
    SELECT unnest(r.permissions) AS permission FROM grants g JOIN roles r ON r.id = g.role_id
    WHERE g.principal_id = @principal_id AND g.scope = 'artifact' AND g.scope_id = @artifact_id::uuid
    UNION ALL
    SELECT unnest(r.permissions) FROM grants g JOIN roles r ON r.id = g.role_id JOIN artifacts a ON a.repo_id = g.scope_id
    WHERE g.principal_id = @principal_id AND g.scope = 'repository' AND a.id = @artifact_id::uuid
    UNION ALL
    SELECT unnest(r.permissions) FROM grants g JOIN roles r ON r.id = g.role_id JOIN artifacts a ON a.org_id = g.scope_id
    WHERE g.principal_id = @principal_id AND g.scope = 'organization' AND a.id = @artifact_id::uuid
    UNION ALL
    SELECT unnest(CASE x.access WHEN 'edit' THEN '{artifacts.read,artifacts.write}'::text[] ELSE '{artifacts.read}'::text[] END)
    FROM (
        SELECT a.repository_access AS access FROM artifacts a
        WHERE a.id = @artifact_id::uuid AND a.repository_access <> 'none' AND EXISTS (
            SELECT 1 FROM grants g JOIN roles r ON r.id = g.role_id
            WHERE g.principal_id = @principal_id AND 'repo.read' = ANY (r.permissions)
              AND ((g.scope = 'repository' AND g.scope_id = a.repo_id) OR (g.scope = 'organization' AND g.scope_id = a.org_id)))
        UNION ALL
        SELECT a.organization_access FROM artifacts a
        WHERE a.id = @artifact_id::uuid AND a.organization_access <> 'none' AND EXISTS (
            SELECT 1 FROM grants g JOIN roles r ON r.id = g.role_id
            WHERE g.principal_id = @principal_id AND 'org.read' = ANY (r.permissions) AND g.scope = 'organization' AND g.scope_id = a.org_id)
    ) x
) p WHERE p.permission = ANY (@permissions::text[])
ORDER BY 1;

-- ArtifactsWith lists the IDs of the repository's artifacts where the principal holds the permission, as
-- ArtifactPermissions answers it, artifacts.read or artifacts.write.
-- name: ArtifactsWith :many
SELECT a.id FROM artifacts a WHERE a.repo_id = @repo_id AND (
    EXISTS (SELECT 1 FROM grants g JOIN roles r ON r.id = g.role_id
            WHERE g.principal_id = @principal_id AND @permission::text = ANY (r.permissions)
              AND ((g.scope = 'artifact' AND g.scope_id = a.id) OR (g.scope = 'repository' AND g.scope_id = a.repo_id)
                OR (g.scope = 'organization' AND g.scope_id = a.org_id)))
    OR ((a.repository_access = 'edit' OR (a.repository_access = 'view' AND @permission::text = 'artifacts.read'))
        AND EXISTS (SELECT 1 FROM grants g JOIN roles r ON r.id = g.role_id
                    WHERE g.principal_id = @principal_id AND 'repo.read' = ANY (r.permissions)
                      AND ((g.scope = 'repository' AND g.scope_id = a.repo_id) OR (g.scope = 'organization' AND g.scope_id = a.org_id))))
    OR ((a.organization_access = 'edit' OR (a.organization_access = 'view' AND @permission::text = 'artifacts.read'))
        AND EXISTS (SELECT 1 FROM grants g JOIN roles r ON r.id = g.role_id
                    WHERE g.principal_id = @principal_id AND 'org.read' = ANY (r.permissions)
                      AND g.scope = 'organization' AND g.scope_id = a.org_id)))
ORDER BY a.id;

-- name: Artifact :one
SELECT a.id, a.org_id, o.slug AS org_slug, a.repo_id, rp.slug AS repo_slug, a.title, a.summary, a.kind, a.repository_access, a.organization_access,
       a.agent_id, ag.display_name AS agent_name, a.task_id, t.number AS task_number, a.branch, a.head, v.media_type,
       a.version, a.created_by, cb.display_name AS created_by_name, a.created_at, a.updated_by, ub.display_name AS updated_by_name, a.updated_at
FROM artifacts a JOIN organizations o ON o.id = a.org_id JOIN repositories rp ON rp.id = a.repo_id
JOIN artifact_versions v ON v.artifact_id = a.id AND v.number = a.head
LEFT JOIN principals ag ON ag.id = a.agent_id
LEFT JOIN agent_tasks t ON t.id = a.task_id
LEFT JOIN principals cb ON cb.id = a.created_by
LEFT JOIN principals ub ON ub.id = a.updated_by
WHERE a.id = $1;

-- Artifacts reads the artifacts ids names, newest change first.
-- name: Artifacts :many
SELECT a.id, a.org_id, o.slug AS org_slug, a.repo_id, rp.slug AS repo_slug, a.title, a.summary, a.kind, a.repository_access, a.organization_access,
       a.agent_id, ag.display_name AS agent_name, a.task_id, t.number AS task_number, a.branch, a.head, v.media_type,
       a.version, a.created_by, cb.display_name AS created_by_name, a.created_at, a.updated_by, ub.display_name AS updated_by_name, a.updated_at
FROM artifacts a JOIN organizations o ON o.id = a.org_id JOIN repositories rp ON rp.id = a.repo_id
JOIN artifact_versions v ON v.artifact_id = a.id AND v.number = a.head
LEFT JOIN principals ag ON ag.id = a.agent_id
LEFT JOIN agent_tasks t ON t.id = a.task_id
LEFT JOIN principals cb ON cb.id = a.created_by
LEFT JOIN principals ub ON ub.id = a.updated_by
WHERE a.id = ANY (@ids::uuid[])
ORDER BY a.updated_at DESC, a.id DESC;

-- InsertArtifact writes an artifact in a repository of its organization, with its branch, or the branch of its task
-- when it names none: no row when the repository is another organization's.
-- name: InsertArtifact :execrows
INSERT INTO artifacts (id, org_id, repo_id, title, summary, kind, repository_access, organization_access, agent_id, task_id, branch, created_by, updated_by)
SELECT @id, @org_id, @repo_id, @title, @summary, @kind, @repository_access, @organization_access, sqlc.narg(agent_id)::uuid,
       sqlc.narg(task_id)::uuid,
       coalesce(nullif(@branch::text, ''), (SELECT b.name FROM agent_tasks t JOIN branches b ON b.id = t.branch_id
                                            WHERE t.id = sqlc.narg(task_id)::uuid), ''),
       @actor::uuid, @actor::uuid
WHERE EXISTS (SELECT 1 FROM repositories WHERE id = @repo_id AND org_id = @org_id);

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
