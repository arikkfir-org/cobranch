-- SetReviewSettings replaces a repository's AI review settings with those of /cobranch-review.yaml at its default
-- branch's new tip.
-- name: SetReviewSettings :exec
INSERT INTO review_settings (repo_id, org_id, enabled, branches, ignore, instructions, include, max_diff_lines)
VALUES (@repo_id, @org_id, @enabled, @branches, @ignore, @instructions, @include, @max_diff_lines)
ON CONFLICT (repo_id) DO UPDATE SET enabled = EXCLUDED.enabled, branches = EXCLUDED.branches, ignore = EXCLUDED.ignore,
    instructions = EXCLUDED.instructions, include = EXCLUDED.include, max_diff_lines = EXCLUDED.max_diff_lines;

-- ReviewSettingsOf finds a repository's AI review settings: none until a push moves its default branch.
-- name: ReviewSettingsOf :one
SELECT enabled, branches, ignore, instructions, include, max_diff_lines FROM review_settings WHERE repo_id = $1;
