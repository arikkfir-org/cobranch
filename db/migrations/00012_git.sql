-- Git hosting: a repository's refs, its packs in the bucket, the pushes that brought them, their commits and the
-- branches they move (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/git-hosting.md, "A
-- push"). Only the push worker writes them, a push in one transaction, so a ref never names an object no listed pack
-- holds. Every row carries its repository's org_id. A push's rows are their own record, named by the push and its
-- pusher, so none carries trigger audit().
-- +goose Up

-- A ref of a repository, a branch (refs/heads/) or a tag (refs/tags/), and the object it names.
CREATE TABLE refs (
    repo_id uuid NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    name text NOT NULL,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    sha text NOT NULL,
    PRIMARY KEY (repo_id, name)
);

-- A pack of a repository's objects, at git/<repo_id>/<name>.pack in the bucket with its .idx beside it: active while
-- fetches list it, superseded once compaction replaces it, and its files deleted a day after that.
CREATE TABLE packs (
    repo_id uuid NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    name text NOT NULL,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    size bigint NOT NULL,
    objects bigint NOT NULL,
    state text NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'superseded')),
    created_at timestamptz NOT NULL DEFAULT now(),
    superseded_at timestamptz,
    PRIMARY KEY (repo_id, name)
);

-- A push that moved a repository's refs: its ID, with which a Git server that lost the answer asks again; who pushed,
-- over which transport; the refs it moved, as {name, old, new}; and its pack, empty for a push that only deletes.
CREATE TABLE pushes (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    repo_id uuid NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    pusher_id uuid REFERENCES principals (id) ON DELETE SET NULL,
    transport text NOT NULL CHECK (transport IN ('https', 'ssh')),
    ref_updates jsonb NOT NULL,
    pack text NOT NULL DEFAULT '',
    received_at timestamptz NOT NULL,
    committed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX pushes_repo ON pushes (repo_id, committed_at);

-- A commit a push brought: its tree, parents, message and signature as written; its author and committer as written,
-- with their times' zones, and the principals behind them; and the push that first brought it.
CREATE TABLE commits (
    repo_id uuid NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    sha text NOT NULL,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    tree text NOT NULL,
    parents text[] NOT NULL,
    message text NOT NULL,
    author_id uuid REFERENCES principals (id) ON DELETE SET NULL,
    author_name text NOT NULL,
    author_email text NOT NULL,
    authored_at timestamptz NOT NULL,
    author_zone text NOT NULL,
    committer_id uuid REFERENCES principals (id) ON DELETE SET NULL,
    committer_name text NOT NULL,
    committer_email text NOT NULL,
    committed_at timestamptz NOT NULL,
    committer_zone text NOT NULL,
    signature text NOT NULL DEFAULT '',
    first_push uuid NOT NULL REFERENCES pushes (id) ON DELETE CASCADE,
    PRIMARY KEY (repo_id, sha)
);
CREATE INDEX commits_author ON commits (author_id);
CREATE INDEX commits_committer ON commits (committer_id);

-- A branch, refs/heads/<name> while it lives, and the commit it is at. Deleting it keeps its row, so what refers to it
-- keeps its history, and the name may live again as another branch.
CREATE TABLE branches (
    id uuid PRIMARY KEY,
    org_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    repo_id uuid NOT NULL REFERENCES repositories (id) ON DELETE CASCADE,
    name text NOT NULL,
    head text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deleted_at timestamptz
);
CREATE UNIQUE INDEX branches_live ON branches (repo_id, name) WHERE deleted_at IS NULL;

-- +goose Down
-- Rolling back forgets every push, and the packs in the bucket with it; the login-less users pushes made stay users.
DROP TABLE branches;
DROP TABLE commits;
DROP TABLE pushes;
DROP TABLE packs;
DROP TABLE refs;
