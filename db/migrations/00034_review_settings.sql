-- The AI reviewer's settings for each repository: a parsed copy of /cobranch-review.yaml at the default branch's tip
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/reviews.md, "/cobranch-review.yaml"). A push
-- that moves the default branch replaces its repository's row in its transaction; a repository without one has the
-- defaults. Like merge_requirements, a parsed copy of a file whose history is Git's carries no trigger audit().
-- +goose Up

CREATE TABLE review_settings (
    repo_id uuid PRIMARY KEY REFERENCES repositories (id) ON DELETE CASCADE,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    enabled boolean NOT NULL,
    branches text[] NOT NULL CHECK (cardinality(branches) > 0),
    ignore text[] NOT NULL,
    instructions text NOT NULL,
    include text[] NOT NULL CHECK (cardinality(include) <= 20),
    max_diff_lines integer NOT NULL CHECK (max_diff_lines BETWEEN 1 AND 20000)
);

-- +goose Down
DROP TABLE review_settings;
