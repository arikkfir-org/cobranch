-- The AI reviewer's approval as a merge requirement: main's /cobranch.yaml may require it of an incoming head, under
-- reviews (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/reviews.md, "Required reviews").
-- A branch that requires it requires reviews, so its approvals are never NULL.
-- +goose Up
ALTER TABLE merge_requirements ADD COLUMN ai boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT merge_requirements_ai_reviews CHECK (NOT ai OR approvals IS NOT NULL);

-- +goose Down
-- Rolling back forgets the requirement; /cobranch.yaml keeps it, and the next push into main stores it again.
ALTER TABLE merge_requirements DROP COLUMN ai;
