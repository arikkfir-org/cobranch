# Cobranch Git object storage

> **Snapshot.** This design lives in its [Git object storage page](https://claude.ai/artifact/87ymbY8L5U3CAKxWL96Wyq),
> linked with Cobranch's other designs; this file is generated from it, with its diagrams, as of 2026-11-01, and synced
> again once Cobranch's shape settles.

**Status**: Proposed 2026-11-01 · [ENG-237](https://linear.app/arikkfir/issue/ENG-237) · Changes the [architecture’s Git
storage](architecture.md#storage) and [Git hosting’s storage](git-hosting.md#storage-and-clean-up) · Makes the [Helm
chart’s](https://claude.ai/artifact/LyMHXgSoNQNgHtHNfEjvA8) `blob.url` optional · Built by
[ENG-237](https://linear.app/arikkfir/issue/ENG-237), in two pull requests

Cobranch keeps Git’s packs through one abstraction with three built-in adapters: by default every pack is a large object
in the PostgreSQL database the installation runs anyway, so one store holds every repository whole and a backup of the
database restores them; a cloud’s bucket takes over for packs that outgrow the database, and a directory serves someone
trying Cobranch out. An operator moves from one adapter to another while the installation keeps serving, without losing
an object.

## Scope

Where Cobranch keeps Git's packs ([ENG-237](https://linear.app/arikkfir/issue/ENG-237)). [Git
hosting](git-hosting.md#storage-and-clean-up) put them in a bucket that every installation had to configure, as the
[architecture](architecture.md#storage) planned; this design keeps that abstraction and gives it three built-in
adapters, with the installation's own PostgreSQL as the default, so an installation configures no store at all and its
database holds every repository whole.

**One abstraction, Go Cloud’s bucket; three adapters behind it, the database by default**

![One abstraction, Go Cloud’s bucket; three adapters behind it, the database by default](git-object-storage/adapters.svg)

Refs, the pack list and every other fact stay in PostgreSQL whichever adapter holds the packs; the Git server’s cache
sits in front of each the same way.

- **One abstraction stays.** The Git server and maintenance keep packs through a bucket of Go Cloud's blob package, as
  they do today: write an object once, read it whole or by range, list by prefix, delete. Nothing above it knows which
  adapter runs.
- **Three adapters, built in.** PostgreSQL's large objects, the default; a cloud's bucket, `s3://`, `gs://` or
  `azblob://`; and a directory, `file://`, for trying Cobranch out. Each is a Go Cloud driver, so each passes Go Cloud's
  own conformance tests.
- **The facts stay where they are.** Refs, the pack list, pushes and commits stay in PostgreSQL whichever adapter holds
  the packs, and each Git server pod keeps its cache of packs in front of the store as before.
- **Only the Git server and maintenance open the store.** The API servers never read a pack: code browsing asks the Git
  server. They stop taking the store's URL.

Choices beyond the agreed designs are collected under [Decisions](#decisions).

## Choosing an adapter

**Which adapter: the database unless its size or its backups argue for a bucket**

![Which adapter: the database unless its size or its backups argue for a bucket](git-object-storage/choose.svg)

Either way the choice can change later: the move between adapters copies every object over while the installation keeps
serving.

| Adapter | `blob.url` | Fits | Backup |
| --- | --- | --- | --- |
| PostgreSQL | empty, the default, or `postgres://` | Every installation, until its packs outgrow its database | The database's, which holds every repository whole |
| A cloud's bucket | `s3://…`, `gs://…`, `azblob://…` | Packs that would outgrow the database, or make its backups too slow | The bucket's versioning or replication, beside the database's, restored to the same moment |
| Local files | `file:///…` on `blob.volume` | Someone trying Cobranch out, on a volume that lasts | The volume's snapshots, beside the database's |

- **The default needs nothing.** The database is there anyway, with its credentials, its backups and its high
  availability; the packs share them. The chart's own PostgreSQL holds them in `postgresql.storage.size`, and an
  external database in whatever the operator gave it.
- **A bucket takes its credentials as before,** from the cloud's workload identity through `serviceAccount.annotations`,
  or from a Secret's variables through `blob.envFromSecret`.
- **A directory must reach every Git server and maintenance,** so it lives on a volume that lasts: one node's, or a
  ReadWriteMany claim. On an `emptyDir` each pod holds its own packs and loses them with the pod, and maintenance leaves
  the packs alone.

## PostgreSQL's adapter

**In PostgreSQL, an object is a row of `blobs` and a large object that holds its bytes**

![In PostgreSQL, an object is a row of blobs and a large object that holds its bytes](git-object-storage/tables.svg)

A pack’s files are `git/<repo>/<upload>/<pack>.pack` and `.idx`, the keys a bucket would name them by; the database
adapter keeps keys as bytes, so any key Go Cloud accepts fits.

- **A table and large objects.** Table `blobs` names each object by its key, as bytes, with its size, MD5, content
  headers, metadata and when it was written; column `content` names the large object that holds its bytes. A large
  object reaches 4 TB and reads by range, where a `bytea` value tops out at 1 GB and reads whole.
- **Keys as bytes.** The key is the string a bucket names the object by, `git/<repo>/<upload>/<pack>.pack`, kept as
  `bytea` so any key Go Cloud accepts fits, NUL included; listing walks them in byte order, by prefix and an optional
  delimiter, a page of 1000 at a time.
- **A write is one transaction.** The adapter creates the large object with the first MiB (`lo_from_bytea`), appends
  each further MiB (`lo_put`), and names it in `blobs` before it commits, so a write that fails or is canceled midway
  leaves nothing behind. A write that may only add, which Go Cloud's `IfNotExist` asks, refuses an existing key.
- **A read takes a MiB at a time,** with `lo_get` at an offset, outside any transaction: objects are written once, under
  keys that never come back. A read of an object deleted under it fails rather than returning part of it.
- **No large object outlives its row.** A trigger on `blobs` calls `lo_unlink` when a row is deleted, or when an
  overwrite points it at another large object, in the same transaction.
- **SQL in `.sql` files,** generated by sqlc like every other query; the migration adds the table and its trigger.
- **What Go Cloud asks and the database can't give:** signed URLs. Nothing in Cobranch uses them, so the adapter answers
  that they aren't supported.

## A push and the sweep

**A push writes its pack in a transaction of its own, then commits the push that lists it**

![A push writes its pack in a transaction of its own, then commits the push that lists it](git-object-storage/push.svg)

The same steps run for every adapter; only where steps 3 to 5 write differs. The repository’s lock is held only while
the push’s own transaction runs, not while its pack is written.

- **The pack is written before the push's transaction,** in transactions of its own, as it is to a bucket: the
  repository's lock is held only while the push commits, not while its pack uploads.
- **A failed push leaves objects no pack lists,** as it does in a bucket, and maintenance deletes them a day later.

**Deleting an object deletes its large object in the same transaction**

![Deleting an object deletes its large object in the same transaction](git-object-storage/delete.svg)

Vacuum reuses the freed pages rather than handing them back to the disk; a database that shrank for good needs the
operator’s `VACUUM FULL` of `pg_largeobject` to give the space back.

- **Deleting frees space for the next writes.** Vacuum makes a deleted large object's pages reusable; the database's
  files don't shrink by themselves. An installation that deleted a great deal for good runs `VACUUM FULL pg_largeobject`
  in a quiet hour to give the disk back.

## Backup and restore

**A backup of the database is a backup of every repository**

![A backup of the database is a backup of every repository](git-object-storage/backup.svg)

Large objects belong to the role that wrote them, and only it may delete them: a restore under another role needs
`ALTER LARGE OBJECT … OWNER TO` before maintenance can sweep.

- **The database is the whole installation.** With the default adapter, a `pg_dump` of the database, or a physical
  backup or snapshot of its server, restores every repository: refs, packs and the facts about them, from the same
  moment. Git servers that start over a restored database fill their empty caches from it.
- **`pg_dump` includes large objects** when it dumps a whole database; one that names schemas or tables needs
  `--large-objects` as well.
- **Restore as the role Cobranch connects as.** Large objects belong to the role that wrote them, and only it may delete
  them; a restore as another role needs `ALTER LARGE OBJECT … OWNER TO` that role before maintenance can sweep.
- **With a bucket or a directory,** the store and the database are backed up apart, and a restore brings both back to
  the same moment: a pack the database lists and the store lacks fails every fetch that needs it.

## Moving between adapters

**Moving to another adapter: reads fall back to the old store while maintenance copies it over**

![Moving to another adapter: reads fall back to the old store while maintenance copies it over](git-object-storage/move.svg)

Nothing is deleted from the old store by the move: until the operator deletes it, going back is setting the two values
the other way round.

- **`blob.moveFrom` names the old store,** `postgres://` for the database, while `blob.url` names the new one. The Git
  server writes only to the new store, reads from it first and falls back to the old one, and deletes from both;
  listings merge the two.
- **Maintenance copies the old store over,** each run, hourly: every object the old store holds and the new one lacks,
  its size and MD5 checked, and logs how many are left. Maintenance compacts and sweeps through the same fallback, so it
  may run meanwhile.
- **The operator finishes the move** once a run logs that none are left: they remove `blob.moveFrom`, roll out, and
  delete the old store when they like. Nothing deletes it for them.
- **Going back** is the same move the other way round, until the old store is deleted.
- **Both stores last and differ.** A `file://` directory on an emptyDir is each pod's own, which maintenance never sees,
  so the chart refuses a move into or out of one; and a store can't move to itself, however its two URLs write it.

## Configuration

| Value | What it does |
| --- | --- |
| `blob.url` | Where packs live: empty (the default) or `postgres://` for the database, a Go Cloud URL for a cloud's bucket, `file:///…` for a directory |
| `blob.moveFrom` | The store packs are moving from, as `blob.url` names one; empty when nothing moves |
| `blob.envFromSecret` | A Secret whose variables give a bucket's driver its credentials, for the Git server and maintenance |
| `blob.volume`, `blob.mountPath` | The volume a `file://` URL's directory is on, and where it is mounted |
| `serviceAccount.annotations` | The Git server's and maintenance's ServiceAccount, bound to a cloud's workload identity for a bucket |
| `postgresql.storage.size` | The chart's own database, which holds the packs too with the default adapter |

## Tests

- **Go Cloud's conformance tests** run against the PostgreSQL adapter, as Go Cloud runs them against its own drivers:
  reads, ranges, writes, attributes, listing with every delimiter, copies, deletes and the odd keys. Its own tests add
  large-object counts across overwrites, deletes and canceled writes, and ranges across chunks.
- **End to end with the real `git`,** over both the database and files: clones, fetches and pushes, compaction and the
  sweep a day later with every large object accounted for, and a copy of the database served by a fresh Git server.
- **The chart in CI** installs in kind with its defaults, so the database adapter runs under the same clone, push and
  compaction checks as before.
- **The move** gets its own end to end: packs pushed into one store, read through the fallback, copied over by
  maintenance, then served with the old store gone.

## Rollout

**Two pull requests: the database by default, then the move between adapters**

![Two pull requests: the database by default, then the move between adapters](git-object-storage/rollout.svg)

An installation that sets `blob.url` keeps its bucket or directory across the upgrade; one that never set it can’t
exist, since the value was required until now.

- **An upgraded installation keeps its store.** `blob.url` was required until now, so every installation names one, and
  keeps it until its operator moves.
- **An installation moves on its own schedule,** by setting `blob.url` empty and `blob.moveFrom` to its bucket once the
  second pull request is out, and its packs then copy into the database over the next maintenance runs.

## Risks

- **The database grows with the packs,** and its backups with it. The cache keeps reads off it; an installation whose
  packs outgrow it moves to a bucket, without losing an object.
- **Every pack is written to the WAL,** so replicas and point-in-time recovery carry it too; a busy installation sizes
  its WAL and its replicas' network for its pushes.
- **A write holds a connection while it uploads.** The pack is on the pod's disk by then, so the upload runs at the
  database's speed; the pool's size bounds how many push at once.
- **During a move's first rollout,** a Git server that hasn't rolled out yet can't read a pack a rolled-out one wrote to
  the new store, and that fetch fails until the rollout ends: move in a quiet moment.
- **A restore under another role** leaves large objects maintenance can't delete, until the operator gives them to
  Cobranch's role.

## Decisions

| Proposal | Why | Decision |
| --- | --- | --- |
| Keep Go Cloud's bucket as the one abstraction, with three built-in adapters: PostgreSQL's large objects, a cloud's bucket and local files | The owner's decision; every caller stays as it is | Agreed |
| PostgreSQL by default: `blob.url` empty keeps packs in the installation's database | One store to run, back up and restore; nothing to configure | Agreed |
| Large objects, not `bytea` rows | Up to 4 TB, read by range, and `pg_dump` keeps them; a `bytea` value tops out at 1 GB and reads whole | Agreed |
| Keys as `bytea`, listed in byte order | Any key Go Cloud accepts fits, and listing matches a bucket's | Proposed |
| A write in one transaction of its own, the pack written before the push's | Nothing half-written survives a failure, and the repository's lock isn't held during the upload | Proposed |
| A trigger unlinks the large object of a deleted or overwritten row | No large object outlives its row, whatever deletes it | Proposed |
| Only the Git server and maintenance take the store's URL | Nothing else reads packs | Proposed |
| `blob.moveFrom`: reads fall back to the old store while maintenance copies it over | An installation moves without stopping or losing an object, and can go back until it deletes the old store | Proposed |
| Signed URLs aren't supported by the database adapter | Nothing in Cobranch uses them | Proposed |

## Open questions

1. **Copy the old store in maintenance, or in a command the operator runs?** Proposed: maintenance, hourly, so a move
   needs no access to the cluster beyond the chart's values, and logs its progress where the operator already looks.

Cobranch is a Kfirs project, created by Arik Kfir.
