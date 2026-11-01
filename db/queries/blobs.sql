-- The object store in PostgreSQL (internal/adapters/postgres/blobs.go): each object a row of blobs and a large object.
-- A write runs in one transaction: CreateLargeObject with the first chunk, AppendLargeObject for the rest, then
-- PutBlob; a read reads the large object a chunk at a time, outside any transaction, as objects are written once.

-- name: CreateLargeObject :one
SELECT lo_from_bytea(0, @data::bytea)::oid AS content;

-- name: AppendLargeObject :exec
SELECT lo_put(@content::oid, @offset_at::bigint, @data::bytea);

-- ReadLargeObject reads up to length bytes of a large object from offset; fewer at its end.
-- name: ReadLargeObject :one
SELECT lo_get(@content::oid, @offset_at::bigint, @length::int)::bytea AS data;

-- PutBlob names a large object by key, replacing the object the key named, whose large object trigger blobs_unlink
-- then deletes.
-- name: PutBlob :exec
INSERT INTO blobs (key, content, size, md5, content_type, cache_control, content_disposition, content_encoding,
    content_language, metadata)
VALUES (@key, @content, @size, @md5, @content_type, @cache_control, @content_disposition, @content_encoding,
    @content_language, @metadata)
ON CONFLICT (key) DO UPDATE SET content = EXCLUDED.content, size = EXCLUDED.size, md5 = EXCLUDED.md5,
    content_type = EXCLUDED.content_type, cache_control = EXCLUDED.cache_control,
    content_disposition = EXCLUDED.content_disposition, content_encoding = EXCLUDED.content_encoding,
    content_language = EXCLUDED.content_language, metadata = EXCLUDED.metadata, written_at = now();

-- AddBlob names a large object by key unless the key names one already, and reports whether it did.
-- name: AddBlob :one
WITH added AS (
    INSERT INTO blobs (key, content, size, md5, content_type, cache_control, content_disposition, content_encoding,
        content_language, metadata)
    VALUES (@key, @content, @size, @md5, @content_type, @cache_control, @content_disposition, @content_encoding,
        @content_language, @metadata)
    ON CONFLICT (key) DO NOTHING
    RETURNING 1
)
SELECT EXISTS (SELECT 1 FROM added);

-- name: GetBlob :one
SELECT * FROM blobs WHERE key = $1;

-- ListBlobs lists, in key order, up to page_size objects whose keys are at least lower and, given until, below it.
-- name: ListBlobs :many
SELECT key, size, md5, written_at FROM blobs
WHERE key >= @lower::bytea AND (sqlc.narg('until')::bytea IS NULL OR key < sqlc.narg('until')::bytea)
ORDER BY key
LIMIT @page_size;

-- DeleteBlob deletes an object; trigger blobs_unlink deletes its large object.
-- name: DeleteBlob :execrows
DELETE FROM blobs WHERE key = $1;
