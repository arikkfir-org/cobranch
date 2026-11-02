-- OrganizationLabels lists an organization's labels by name, whatever its case, with how many issues carry each.
-- name: OrganizationLabels :many
SELECT sqlc.embed(l), (SELECT count(*) FROM issue_labels il WHERE il.label_id = l.id) AS issues
FROM labels l
WHERE l.org_id = @org_id
ORDER BY lower(l.name), l.id;

-- LabelByID finds one of an organization's labels, with how many issues carry it.
-- name: LabelByID :one
SELECT sqlc.embed(l), (SELECT count(*) FROM issue_labels il WHERE il.label_id = l.id) AS issues
FROM labels l
WHERE l.org_id = @org_id AND l.id = @id;

-- OrganizationLabelsOf finds those of the IDs that are an organization's labels.
-- name: OrganizationLabelsOf :many
SELECT id, name, color FROM labels WHERE org_id = @org_id AND id = ANY (@ids::uuid[]) ORDER BY lower(name), id;

-- name: InsertLabel :exec
INSERT INTO labels (id, org_id, name, color, description, created_at, updated_at)
VALUES (@id, @org_id, @name, @color, @description, @created_at, @created_at);

-- UpdateLabel writes a label's fields over the version it was read at: no row when it is at another now.
-- name: UpdateLabel :one
UPDATE labels SET name = @name, color = @color, description = @description, updated_at = now()
WHERE id = @id AND version = @version
RETURNING version, updated_at;

-- DeleteLabel deletes a label at the version it was read at, and so takes it off its issues: no row when it is at
-- another now.
-- name: DeleteLabel :execrows
DELETE FROM labels WHERE id = @id AND version = @version;

-- RenewLabelledIssues counts a change on every issue that carries the label, which shows its name and colour, so
-- that their ETags change with them.
-- name: RenewLabelledIssues :exec
UPDATE issues SET version = version WHERE id IN (SELECT issue_id FROM issue_labels WHERE label_id = @label_id);

-- LabelIssue puts the labels on an issue; those it carries already stay.
-- name: LabelIssue :exec
INSERT INTO issue_labels (issue_id, label_id) SELECT @issue_id, unnest(@label_ids::uuid[]) ON CONFLICT DO NOTHING;

-- UnlabelIssue takes every label but those named off an issue.
-- name: UnlabelIssue :exec
DELETE FROM issue_labels WHERE issue_id = @issue_id AND NOT (label_id = ANY (@keep::uuid[]));
