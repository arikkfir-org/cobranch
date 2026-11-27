-- Every agent sees its organization: an agent made without a role at its organization that reads it couldn't read the
-- conversations it is asked in, so asking it was refused (ask.unreadable). New agents get Guest when made; this gives
-- it to those made before
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/agents.md, "The agent principal").
-- +goose Up
INSERT INTO grants (principal_id, role_id, scope, scope_id, source)
SELECT a.principal_id, '00000000-0000-7000-8000-000000000203', 'organization', a.org_id, 'grant'
FROM agents a
WHERE NOT EXISTS (
    SELECT FROM grants g JOIN roles r ON r.id = g.role_id
    WHERE g.principal_id = a.principal_id AND g.scope = 'organization' AND g.scope_id = a.org_id
        AND 'org.read' = ANY (r.permissions)
)
ON CONFLICT DO NOTHING;

-- +goose Down
-- Rolling back keeps the grants: they are ordinary grants now, which an admin revokes like any other.
SELECT 1;
