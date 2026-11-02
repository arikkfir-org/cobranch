-- ReviewsOf lists the reviews on those of a repository's commits that shas name, by commit and in the order they were
-- submitted, with the display names and kinds of their reviewers.
-- name: ReviewsOf :many
SELECT sqlc.embed(r), p.display_name AS reviewer_display_name, p.kind AS reviewer_kind
FROM reviews r LEFT JOIN principals p ON p.id = r.reviewer_id
WHERE r.repo_id = @repo_id AND r.sha = ANY (@shas::text[])
ORDER BY r.sha, r.submitted_at, r.id;

-- ReviewOf finds one of a repository's reviews, with the display name and kind of its reviewer.
-- name: ReviewOf :one
SELECT sqlc.embed(r), p.display_name AS reviewer_display_name, p.kind AS reviewer_kind
FROM reviews r LEFT JOIN principals p ON p.id = r.reviewer_id
WHERE r.repo_id = $1 AND r.id = $2;

-- name: InsertReview :one
INSERT INTO reviews (id, org_id, repo_id, sha, reviewer_id, state, body)
VALUES (@id, @org_id, @repo_id, @sha, @reviewer_id, @state, @body)
RETURNING *;

-- HeadAuthors finds who is behind those of a repository's commits that shas name: the principal behind each one's
-- author, and the pusher of the push that first brought it, whose approvals of it don't count.
-- name: HeadAuthors :many
SELECT c.sha, c.author_id, p.pusher_id
FROM commits c JOIN pushes p ON p.id = c.first_push
WHERE c.repo_id = @repo_id AND c.sha = ANY (@shas::text[]);
