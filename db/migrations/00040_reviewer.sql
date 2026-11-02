-- The built-in AI reviewer: an agent of every organization that reviews every push of a branch, holding Automation
-- there, and the review tasks Cobranch starts for it, each at the head it reviews
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/reviews.md, "The AI reviewer"). Nothing
-- starts review tasks yet: the push worker will queue them.
-- +goose Up

-- Automation at an organization: what the repositories' Automation holds, in every repository of the organization,
-- and seeing the organization itself, so an agent that holds it reads, annotates, reviews and posts wherever it is.
INSERT INTO roles (id, scope, name, description, permissions, built_in) VALUES
    ('00000000-0000-7000-8000-000000000204', 'organization', 'Automation', 'Reads, annotates, reviews and posts in every repository, as the AI reviewer does.',
     '{org.read,repo.read,annotations.write,reviews.submit,messages.post}', true);

-- What Cobranch made a built-in agent for: 'review' for the AI reviewer. An organization has one agent for each.
ALTER TABLE agents ADD COLUMN purpose text CHECK (purpose IN ('review'));
ALTER TABLE agents ADD CONSTRAINT agents_purpose_built_in CHECK (purpose IS NULL OR built_in);
CREATE UNIQUE INDEX agents_purpose ON agents (org_id, purpose) WHERE purpose IS NOT NULL;

-- A review task reviews one head of a branch: the commit it names.
INSERT INTO task_kinds (kind) VALUES ('review') ON CONFLICT DO NOTHING;
ALTER TABLE agent_tasks ADD COLUMN sha text CHECK (sha ~ '^[0-9a-f]{40}$');
ALTER TABLE agent_tasks ADD CONSTRAINT agent_tasks_review_head CHECK (kind <> 'review' OR (sha IS NOT NULL AND branch_id IS NOT NULL));

-- Every organization already here gets its reviewer, as every new one does when it is created, named AI reviewer or,
-- where one of its agents has that name, the first of AI reviewer 2, 3 and on that is free.
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
-- Rolling back deletes the reviewers, with their grants and tasks, and the other review tasks; an organization whose
-- members joined as Automation joins them as Member again. The audit log keeps what they did.
DELETE FROM principals WHERE id IN (SELECT principal_id FROM agents WHERE purpose IS NOT NULL);
DELETE FROM agent_tasks WHERE kind = 'review';
DELETE FROM task_kinds WHERE kind = 'review';
ALTER TABLE agent_tasks DROP COLUMN sha;
DROP INDEX agents_purpose;
ALTER TABLE agents DROP COLUMN purpose;
UPDATE organizations SET membership_role_id = '00000000-0000-7000-8000-000000000202'
WHERE membership_role_id = '00000000-0000-7000-8000-000000000204';
DELETE FROM roles WHERE id = '00000000-0000-7000-8000-000000000204';
