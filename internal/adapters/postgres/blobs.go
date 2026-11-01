package postgres

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // Go Cloud's attributes carry an MD5, as buckets' do; nothing trusts it as a signature
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"gocloud.dev/blob"
	"gocloud.dev/blob/driver"
	"gocloud.dev/gcerrors"

	"cobranch.dev/internal/adapters/postgres/queries"
)

// blobChunk is how many bytes of a large object one statement writes or reads.
const blobChunk = 1 << 20

var (
	// errBlobNotFound is a key that names no object.
	errBlobNotFound = errors.New("no object has this key")
	// errBlobExists is a write that may only add an object, to a key that names one already.
	errBlobExists = errors.New("an object has this key already")
	// errEmptyKey is a write to the empty key, which names no object.
	errEmptyKey = errors.New("an object's key is never empty")
	// errNoSignedURLs is a signed URL asked of the database, which can't sign one.
	errNoSignedURLs = errors.New("objects in PostgreSQL have no signed URLs: stream them instead")
)

// exposeNothing is the As function the store gives its callers' hooks: it exposes nothing of the store.
func exposeNothing(any) bool { return false }

// before runs a caller's hook, nil or BeforeRead, BeforeWrite and the rest, before the operation it precedes.
func before(hook func(asFunc func(any) bool) error) error {
	if hook == nil {
		return nil
	}
	return hook(exposeNothing)
}

// Bucket returns the object store in pool's database, as a Go Cloud bucket: each object a row of table blobs and a
// large object that holds its bytes. It is the store's default adapter, so an installation keeps its Git packs in the
// database it runs anyway, and a backup of that database holds every repository whole. Closing the bucket leaves the
// pool open.
func Bucket(pool *pgxpool.Pool) *blob.Bucket {
	return blob.NewBucket(&blobStore{pool: pool, q: queries.New(pool)})
}

// blobStore is Go Cloud's driver over table blobs (db/migrations/00018_blobs.sql). A write runs in one transaction,
// which creates the large object a chunk at a time and names it by its key, so an object is either whole or absent; a
// read reads the large object a chunk at a time outside any transaction, since an object never changes once written.
type blobStore struct {
	pool *pgxpool.Pool
	q    *queries.Queries
}

var _ driver.Bucket = (*blobStore)(nil)

// ErrorCode maps the store's errors to Go Cloud's codes.
func (s *blobStore) ErrorCode(err error) gcerrors.ErrorCode {
	switch {
	case errors.Is(err, errBlobNotFound):
		return gcerrors.NotFound
	case errors.Is(err, errBlobExists):
		return gcerrors.FailedPrecondition
	case errors.Is(err, errEmptyKey):
		return gcerrors.InvalidArgument
	case errors.Is(err, errNoSignedURLs):
		return gcerrors.Unimplemented
	case errors.Is(err, context.Canceled):
		return gcerrors.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return gcerrors.DeadlineExceeded
	default:
		return gcerrors.Unknown
	}
}

// As exposes nothing of the store.
func (s *blobStore) As(i any) bool { return exposeNothing(i) }

// ErrorAs exposes nothing of the store's errors.
func (s *blobStore) ErrorAs(error, any) bool { return false }

// Attributes reads an object's attributes.
func (s *blobStore) Attributes(ctx context.Context, key string) (*driver.Attributes, error) {
	row, err := s.get(ctx, key)
	if err != nil {
		return nil, err
	}
	var metadata map[string]string
	if row.Metadata != nil {
		if err := json.Unmarshal(row.Metadata, &metadata); err != nil {
			return nil, fmt.Errorf("reading the metadata of object %q: %w", key, err)
		}
	}
	return &driver.Attributes{CacheControl: row.CacheControl, ContentDisposition: row.ContentDisposition,
		ContentEncoding: row.ContentEncoding, ContentLanguage: row.ContentLanguage, ContentType: row.ContentType,
		Metadata: metadata, CreateTime: row.WrittenAt, ModTime: row.WrittenAt, Size: row.Size, MD5: row.Md5,
		ETag: fmt.Sprintf(`"%x"`, row.Md5)}, nil
}

// get reads an object's row, or errBlobNotFound.
func (s *blobStore) get(ctx context.Context, key string) (queries.Blob, error) {
	row, err := s.q.GetBlob(ctx, []byte(key))
	if errors.Is(err, pgx.ErrNoRows) {
		return queries.Blob{}, fmt.Errorf("reading object %q: %w", key, errBlobNotFound)
	}
	if err != nil {
		return queries.Blob{}, fmt.Errorf("reading object %q: %w", key, err)
	}
	return row, nil
}

// NewRangeReader reads length bytes of an object from offset, or to its end when length is negative.
func (s *blobStore) NewRangeReader(ctx context.Context, key string, offset, length int64, opts *driver.ReaderOptions) (driver.Reader, error) {
	if err := before(opts.BeforeRead); err != nil {
		return nil, err
	}
	row, err := s.get(ctx, key)
	if err != nil {
		return nil, err
	}
	end := row.Size
	if length >= 0 && offset+length < end {
		end = offset + length
	}
	return &blobReader{ctx: ctx, q: s.q, key: key, content: row.Content, at: min(offset, end), end: end,
		attrs: driver.ReaderAttributes{ContentType: row.ContentType, ModTime: row.WrittenAt, Size: row.Size}}, nil
}

// NewTypedWriter writes an object, which its key names once the writer closes.
func (s *blobStore) NewTypedWriter(ctx context.Context, key, contentType string, opts *driver.WriterOptions) (driver.Writer, error) {
	if key == "" {
		return nil, errEmptyKey
	}
	if err := before(opts.BeforeWrite); err != nil {
		return nil, err
	}
	var metadata []byte
	if len(opts.Metadata) > 0 {
		var err error
		if metadata, err = json.Marshal(opts.Metadata); err != nil {
			return nil, fmt.Errorf("writing the metadata of object %q: %w", key, err)
		}
	}
	return &blobWriter{ctx: ctx, s: s, ifNotExist: opts.IfNotExist, hash: md5.New(), //nolint:gosec // see the import
		row: queries.PutBlobParams{Key: []byte(key), ContentType: contentType, CacheControl: opts.CacheControl,
			ContentDisposition: opts.ContentDisposition, ContentEncoding: opts.ContentEncoding,
			ContentLanguage: opts.ContentLanguage, Metadata: metadata}}, nil
}

// Copy writes a copy of the object srcKey names under dstKey, in one transaction.
func (s *blobStore) Copy(ctx context.Context, dstKey, srcKey string, opts *driver.CopyOptions) error {
	if err := before(opts.BeforeCopy); err != nil {
		return err
	}
	src, err := s.get(ctx, srcKey)
	if err != nil {
		return err
	}
	w := &blobWriter{ctx: ctx, s: s, hash: md5.New(), //nolint:gosec // see the import
		row: queries.PutBlobParams{Key: []byte(dstKey), ContentType: src.ContentType, CacheControl: src.CacheControl,
			ContentDisposition: src.ContentDisposition, ContentEncoding: src.ContentEncoding,
			ContentLanguage: src.ContentLanguage, Metadata: src.Metadata}}
	r := &blobReader{ctx: ctx, q: s.q, key: srcKey, content: src.Content, end: src.Size}
	if _, err := io.Copy(w, r); err != nil {
		w.abort()
		return fmt.Errorf("copying object %q to %q: %w", srcKey, dstKey, err)
	}
	return w.finish(ctx)
}

// Delete deletes an object, and with it its large object.
func (s *blobStore) Delete(ctx context.Context, key string, opts *driver.DeleteOptions) error {
	if err := before(opts.BeforeDelete); err != nil {
		return err
	}
	n, err := s.q.DeleteBlob(ctx, []byte(key))
	if err != nil {
		return fmt.Errorf("deleting object %q: %w", key, err)
	}
	if n == 0 {
		return fmt.Errorf("deleting object %q: %w", key, errBlobNotFound)
	}
	return nil
}

// SignedURL is not one the database can sign: the servers stream an object instead.
func (s *blobStore) SignedURL(context.Context, string, *driver.SignedURLOptions) (string, error) {
	return "", errNoSignedURLs
}

// Close leaves the pool, which is its owner's, open.
func (s *blobStore) Close() error { return nil }

// blobReader reads a large object a chunk at a time, from at to end.
type blobReader struct {
	ctx     context.Context
	q       *queries.Queries
	key     string
	content pgtype.Uint32
	at, end int64
	buf     []byte
	attrs   driver.ReaderAttributes
}

func (r *blobReader) Read(p []byte) (int, error) {
	if len(r.buf) == 0 {
		if r.at >= r.end {
			return 0, io.EOF
		}
		data, err := r.q.ReadLargeObject(r.ctx, r.content, r.at, int32(min(blobChunk, r.end-r.at))) //nolint:gosec // at most blobChunk
		if err != nil {
			return 0, fmt.Errorf("reading object %q at %d: %w", r.key, r.at, err)
		}
		if len(data) == 0 {
			return 0, fmt.Errorf("reading object %q at %d: %w", r.key, r.at, io.ErrUnexpectedEOF)
		}
		r.buf, r.at = data, r.at+int64(len(data))
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

func (r *blobReader) Close() error                         { return nil }
func (r *blobReader) Attributes() *driver.ReaderAttributes { return &r.attrs }
func (r *blobReader) As(any) bool                          { return false }

// blobWriter writes a large object a chunk at a time in one transaction, run by transact in a goroutine of its own from
// the first chunk on, which the writer feeds the chunks one by one, and names it by its key as the writer closes. A
// canceled context, a failure or an abort rolls the transaction back, large object and all.
type blobWriter struct {
	ctx        context.Context
	s          *blobStore
	row        queries.PutBlobParams
	ifNotExist bool
	hash       hash.Hash
	buf        []byte
	// chunks feeds the transaction, from the first chunk on; closed, it ends the transaction, which commits only if
	// complete is set by then.
	chunks   chan []byte
	done     chan error
	complete bool
	// ended is set once the transaction's result was read from done.
	ended bool
	err   error
}

// errAborted is the transaction of a writer that was abandoned before it closed.
var errAborted = errors.New("the write was abandoned")

func (w *blobWriter) Write(p []byte) (int, error) {
	written := 0
	for w.err == nil && len(p) > 0 {
		take := min(blobChunk-len(w.buf), len(p))
		w.buf = append(w.buf, p[:take]...)
		p, written = p[take:], written+take
		if len(w.buf) == blobChunk {
			w.err = w.flush()
		}
	}
	return written, w.err
}

// flush hands the buffered chunk to the transaction, which the first chunk starts; it fails only if the transaction
// ended already, with the transaction's error.
func (w *blobWriter) flush() error {
	if w.chunks == nil {
		w.chunks, w.done = make(chan []byte), make(chan error, 1)
		go func() {
			w.done <- transact(w.ctx, w.s.pool, w.s.q, fmt.Sprintf("writing object %q", w.row.Key), uuid.Nil, w.write)
		}()
	}
	chunk := w.buf
	if chunk == nil {
		chunk = []byte{} // nil would be NULL, which makes no large object: an empty object's is empty bytes
	}
	w.buf = nil                // the chunk is the transaction's now
	_, _ = w.hash.Write(chunk) // a hash.Hash never fails
	w.row.Size += int64(len(chunk))
	select {
	case w.chunks <- chunk:
		return nil
	case err := <-w.done:
		w.ended = true
		return err
	}
}

// write is the writer's transaction: it writes each chunk to the large object, the first creating it, and once the
// chunks end, names it by the object's key, unless the write was abandoned or its context ended.
func (w *blobWriter) write(ctx context.Context, q *queries.Queries) error {
	var content pgtype.Uint32
	var at int64
	for chunk := range w.chunks {
		var err error
		if !content.Valid {
			content, err = q.CreateLargeObject(ctx, chunk)
		} else {
			err = q.AppendLargeObject(ctx, content, at, chunk)
		}
		if err != nil {
			return fmt.Errorf("writing at %d: %w", at, err)
		}
		at += int64(len(chunk))
	}
	if !w.complete {
		return errAborted
	}
	if err := ctx.Err(); err != nil {
		return err //nolint:wrapcheck // transact wraps it with the object's key
	}
	row := w.row
	row.Content = content
	if !w.ifNotExist {
		return q.PutBlob(ctx, row) //nolint:wrapcheck // transact wraps it with the object's key
	}
	added, err := q.AddBlob(ctx, queries.AddBlobParams(row))
	if err != nil {
		return err //nolint:wrapcheck // transact wraps it with the object's key
	}
	if !added {
		return errBlobExists
	}
	return nil
}

// Close writes what is buffered, names the object by its key and commits, unless the context was canceled or a write
// failed: then it rolls back, and the key keeps what it named.
func (w *blobWriter) Close() error { return w.finish(w.ctx) }

// finish closes the writer on ctx, the writer's own or a copy's.
func (w *blobWriter) finish(ctx context.Context) error {
	if w.err != nil {
		w.abort()
		return w.err
	}
	if err := ctx.Err(); err != nil {
		w.abort()
		return fmt.Errorf("writing object %q: %w", w.row.Key, err)
	}
	if len(w.buf) > 0 || w.chunks == nil {
		if err := w.flush(); err != nil {
			return err
		}
	}
	w.row.Md5 = w.hash.Sum(nil)
	w.complete = true
	close(w.chunks)
	w.ended = true
	return <-w.done
}

// abort ends the writer's transaction, which rolls back, a no-op before it began or once it ended.
func (w *blobWriter) abort() {
	if w.chunks == nil || w.ended {
		return
	}
	close(w.chunks)
	w.ended = true
	<-w.done //nolint:errcheck // errAborted, or the failure that ended it first: the write is abandoned either way
}

// blobPage is how many objects a list returns at most when its caller sets no page size.
const blobPage = 1000

// ListPaged lists a page of objects in key order, those whose keys start with opts.Prefix, a key that goes on past
// opts.Delimiter after the prefix folded into one directory with every other such key. The page token is the key the
// next page starts at.
func (s *blobStore) ListPaged(ctx context.Context, opts *driver.ListOptions) (*driver.ListPage, error) {
	if err := before(opts.BeforeList); err != nil {
		return nil, err
	}
	size := opts.PageSize
	if size <= 0 {
		size = blobPage
	}
	prefix := []byte(opts.Prefix)
	lower, until := prefix, keyAfter(prefix)
	if len(opts.PageToken) > 0 && bytes.Compare(opts.PageToken, lower) > 0 {
		lower = opts.PageToken
	}
	page := &driver.ListPage{}
	for lower != nil && len(page.Objects) < size {
		rows, err := s.q.ListBlobs(ctx, lower, until, int32(size-len(page.Objects))) //nolint:gosec // a page's size
		if err != nil {
			return nil, fmt.Errorf("listing objects under %q: %w", opts.Prefix, err)
		}
		if len(rows) == 0 {
			return page, nil
		}
		lower = listRows(page, rows, prefix, []byte(opts.Delimiter))
	}
	if lower == nil {
		return page, nil
	}
	more, err := s.q.ListBlobs(ctx, lower, until, 1)
	if err != nil {
		return nil, fmt.Errorf("listing objects under %q: %w", opts.Prefix, err)
	}
	if len(more) > 0 {
		page.NextPageToken = lower
	}
	return page, nil
}

// listRows adds rows to page, the keys under one directory as the directory alone, and returns the key the listing
// goes on from: right after the last row's key, or after every key in the directory it stopped at; nil when no key
// can follow.
func listRows(page *driver.ListPage, rows []queries.ListBlobsRow, prefix, delimiter []byte) []byte {
	var next []byte
	for _, row := range rows {
		if i := bytes.Index(row.Key[len(prefix):], delimiter); len(delimiter) > 0 && i >= 0 {
			dir := row.Key[:len(prefix)+i+len(delimiter)]
			page.Objects = append(page.Objects, &driver.ListObject{Key: string(dir), IsDir: true})
			return keyAfter(dir)
		}
		page.Objects = append(page.Objects, &driver.ListObject{Key: string(row.Key), ModTime: row.WrittenAt,
			Size: row.Size, MD5: row.Md5})
		next = append(bytes.Clone(row.Key), 0)
	}
	return next
}

// keyAfter returns the smallest key that comes after every key starting with prefix, or nil when none does: an empty
// prefix, or one of 0xff bytes alone.
func keyAfter(prefix []byte) []byte {
	after := bytes.Clone(prefix)
	for i := len(after) - 1; i >= 0; i-- {
		if after[i] < 0xff {
			after[i]++
			return after[:i+1]
		}
	}
	return nil
}
