-- The object store in PostgreSQL, its default adapter: every object Cobranch keeps under a key, such as a Git pack, is
-- a large object here, so the database alone holds every repository whole, and its backups with it
-- (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/git-object-storage.md). An installation
-- whose blob.url names a bucket or a directory leaves this table empty.
-- +goose Up

-- An object: its key, the large object that holds its bytes, and the attributes Go Cloud's blob package keeps with it.
-- Keys are bytes, compared byte by byte as Go Cloud lists them, and may hold any UTF-8, NUL included; metadata is
-- its JSON, for the same reason. A row is written once, in the transaction that wrote its large object.
CREATE TABLE blobs (
    key bytea PRIMARY KEY,
    content oid NOT NULL,
    size bigint NOT NULL,
    md5 bytea NOT NULL,
    content_type text NOT NULL DEFAULT '',
    cache_control text NOT NULL DEFAULT '',
    content_disposition text NOT NULL DEFAULT '',
    content_encoding text NOT NULL DEFAULT '',
    content_language text NOT NULL DEFAULT '',
    metadata bytea,
    written_at timestamptz NOT NULL DEFAULT now()
);

-- blobs_unlink deletes the large object a row no longer names, deleted or overwritten, in the same transaction: no
-- large object outlives the row that names it.
-- +goose StatementBegin
CREATE FUNCTION blobs_unlink() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM lo_unlink(OLD.content);
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER unlink_deleted AFTER DELETE ON blobs FOR EACH ROW EXECUTE FUNCTION blobs_unlink();
CREATE TRIGGER unlink_overwritten AFTER UPDATE OF content ON blobs FOR EACH ROW
    WHEN (OLD.content IS DISTINCT FROM NEW.content) EXECUTE FUNCTION blobs_unlink();

-- +goose Down
-- Rolling back deletes every object the database holds, every repository's packs with them.
DELETE FROM blobs;
DROP TABLE blobs;
DROP FUNCTION blobs_unlink();
