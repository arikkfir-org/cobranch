-- Activity: what happened in an organization's repositories, from its events and its deliveries' ends
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/architecture.md, "Events and background
-- work").
-- +goose Up

-- The organization's activity reads its deliveries that ended, newest first, beside its events (events_org).
CREATE INDEX deliveries_ended ON deliveries (org_id, ended_at) WHERE ended_at IS NOT NULL;

-- The Console's Activity page takes the slug activity under an organization, so a repository that had it before moves
-- to activity-repository, or the first of activity-repository-2, -3 and on that its organization has free. Its old
-- slug stays among its slugs, so its API and Git URLs still lead to it.
-- +goose StatementBegin
DO $$
DECLARE
    r record;
    candidate text;
    n int;
BEGIN
    FOR r IN SELECT id, org_id, slug FROM repositories WHERE slug = 'activity' LOOP
        candidate := r.slug || '-repository';
        n := 1;
        WHILE EXISTS (SELECT 1 FROM repositories WHERE org_id = r.org_id AND slug = candidate) LOOP
            n := n + 1;
            candidate := r.slug || '-repository-' || n;
        END LOOP;
        UPDATE repositories SET slug = candidate WHERE id = r.id;
        INSERT INTO repository_slugs (org_id, slug, repo_id) VALUES (r.org_id, candidate, r.id)
        ON CONFLICT (org_id, slug) DO UPDATE SET repo_id = EXCLUDED.repo_id, created_at = now();
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- Rolling back keeps the repositories where they moved: their slugs lead to them either way.
DROP INDEX deliveries_ended;
