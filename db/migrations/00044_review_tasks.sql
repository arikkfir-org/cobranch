-- Review tasks on every push: the push worker queues the AI reviewer's review of each branch head a push brings, and
-- cancels the review of a head the branch moved on from
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/reviews.md, "The AI reviewer"). It needs
-- nothing new of the schema, but every organization's reviewer: servers still on older code while 00040 rolled out
-- created organizations without one, so this gives them theirs as 00040 did, skipping every organization that has it.
-- +goose Up
-- +goose StatementBegin
DO $$
DECLARE
    o record;
    reviewer uuid;
    candidate text;
    n int;
BEGIN
    FOR o IN SELECT id FROM organizations org WHERE NOT EXISTS (SELECT 1 FROM agents a WHERE a.org_id = org.id AND a.purpose = 'review') LOOP
        candidate := 'AI reviewer';
        n := 1;
        WHILE EXISTS (SELECT 1 FROM agents WHERE org_id = o.id AND lower(name) = lower(candidate)) LOOP
            n := n + 1;
            candidate := 'AI reviewer ' || n;
        END LOOP;
        reviewer := uuidv7();
        INSERT INTO principals (id, kind, display_name, org_id) VALUES (reviewer, 'agent', candidate, o.id);
        INSERT INTO agents (principal_id, org_id, name, description, built_in, purpose)
        VALUES (reviewer, o.id, candidate, 'Reviews every push of a branch, as main''s /cobranch-review.yaml says, once it has a model.', true, 'review');
        INSERT INTO grants (principal_id, role_id, scope, scope_id, source)
        VALUES (reviewer, '00000000-0000-7000-8000-000000000204', 'organization', o.id, 'grant');
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- The reviewers stay: 00040's down removes them.
SELECT 1;
