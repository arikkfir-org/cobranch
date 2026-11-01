package blob

import (
	"bytes"
	"cmp"
	"context"
	"crypto/md5" //nolint:gosec // checks a copy against the MD5 the stores keep, as buckets do; no signature
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	"gocloud.dev/blob"
	"gocloud.dev/blob/driver"
	"gocloud.dev/gcerrors"
)

// listPage is how many objects a page of a moving store's listing holds when its caller asks no size.
const listPage = 1000

var (
	// errExists is a write that may only add an object, to a key that names one in the old store.
	errExists = errors.New("an object has this key already, in the store objects are moving from")
	// errBadToken is a page token that this store's listing didn't write.
	errBadToken = errors.New("the page token is not this listing's")
)

// Moved counts what a move did: the objects it copied into the new store, and those the old store holds that the new
// one still lacks.
type Moved struct {
	Copied int
	Left   int
}

// moving is the object store while an installation moves its objects from one store to another: Go Cloud's driver over
// both, so the Git server and maintenance keep working as before, unaware of the move. It writes to the new store only,
// reads from it first and falls back to the old one, deletes from both, and merges their listings. Maintenance copies
// the old store over in the meantime (Move). It closes neither store: their opener does.
type moving struct {
	to, from *blob.Bucket
}

var _ driver.Bucket = (*moving)(nil)

// exposeNothing is the As function a moving store gives its callers' hooks: it exposes neither store.
func exposeNothing(any) bool { return false }

// before runs a caller's hook, nil or BeforeRead, BeforeWrite and the rest, before the operation it precedes.
func before(hook func(asFunc func(any) bool) error) error {
	if hook == nil {
		return nil
	}
	return hook(exposeNothing)
}

// isNotFound reports whether err is a store's answer that no object has the key.
func isNotFound(err error) bool { return gcerrors.Code(err) == gcerrors.NotFound }

// ErrorCode maps the stores' errors, which their buckets already gave Go Cloud's codes, and the moving store's own.
func (m *moving) ErrorCode(err error) gcerrors.ErrorCode {
	switch {
	case errors.Is(err, errExists):
		return gcerrors.FailedPrecondition
	case errors.Is(err, errBadToken):
		return gcerrors.InvalidArgument
	default:
		return gcerrors.Code(err)
	}
}

// As exposes nothing: callers don't reach either store through it.
func (m *moving) As(any) bool { return false }

// ErrorAs exposes nothing either.
func (m *moving) ErrorAs(error, any) bool { return false }

// Attributes reads an object's attributes from the new store, or from the old one when the new one lacks it.
func (m *moving) Attributes(ctx context.Context, key string) (*driver.Attributes, error) {
	a, err := m.to.Attributes(ctx, key)
	if isNotFound(err) {
		a, err = m.from.Attributes(ctx, key)
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // the store's own error, whose code Go Cloud reads
	}
	return &driver.Attributes{
		CacheControl: a.CacheControl, ContentDisposition: a.ContentDisposition, ContentEncoding: a.ContentEncoding,
		ContentLanguage: a.ContentLanguage, ContentType: a.ContentType, Metadata: a.Metadata, CreateTime: a.CreateTime,
		ModTime: a.ModTime, Size: a.Size, MD5: a.MD5, ETag: a.ETag, AsFunc: exposeNothing,
	}, nil
}

// NewRangeReader reads part of an object from the new store, or from the old one when the new one lacks it.
func (m *moving) NewRangeReader(ctx context.Context, key string, offset, length int64, opts *driver.ReaderOptions) (driver.Reader, error) {
	if err := before(opts.BeforeRead); err != nil {
		return nil, err
	}
	r, err := m.to.NewRangeReader(ctx, key, offset, length, nil)
	if isNotFound(err) {
		r, err = m.from.NewRangeReader(ctx, key, offset, length, nil)
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // the store's own error, whose code Go Cloud reads
	}
	return reader{r}, nil
}

// reader is a store's reader, as a driver's.
type reader struct{ *blob.Reader }

// Attributes returns what the store's reader knows of its object.
func (r reader) Attributes() *driver.ReaderAttributes {
	return &driver.ReaderAttributes{ContentType: r.ContentType(), ModTime: r.ModTime(), Size: r.Size()}
}

// As exposes nothing of the store.
func (r reader) As(any) bool { return false }

// NewTypedWriter writes an object to the new store. A write that may only add refuses a key either store holds.
func (m *moving) NewTypedWriter(ctx context.Context, key, contentType string, opts *driver.WriterOptions) (driver.Writer, error) {
	if err := before(opts.BeforeWrite); err != nil {
		return nil, err
	}
	if opts.IfNotExist {
		old, err := m.from.Exists(ctx, key)
		if err != nil {
			return nil, err //nolint:wrapcheck // the store's own error, whose code Go Cloud reads
		}
		if old {
			return nil, errExists
		}
	}
	return m.to.NewWriter(ctx, key, &blob.WriterOptions{ //nolint:wrapcheck // the store's own error, whose code Go Cloud reads
		BufferSize: opts.BufferSize, MaxConcurrency: opts.MaxConcurrency, CacheControl: opts.CacheControl,
		ContentDisposition: opts.ContentDisposition, ContentEncoding: opts.ContentEncoding,
		ContentLanguage: opts.ContentLanguage, ContentType: contentType, DisableContentTypeDetection: true,
		ContentMD5: opts.ContentMD5, Metadata: opts.Metadata, IfNotExist: opts.IfNotExist,
	})
}

// Copy copies an object within the new store, or from the old store into the new one when only the old one holds it.
func (m *moving) Copy(ctx context.Context, dstKey, srcKey string, opts *driver.CopyOptions) error {
	if err := before(opts.BeforeCopy); err != nil {
		return err
	}
	err := m.to.Copy(ctx, dstKey, srcKey, nil)
	if isNotFound(err) {
		return copyObject(ctx, m.to, dstKey, m.from, srcKey)
	}
	return err //nolint:wrapcheck // the store's own error, whose code Go Cloud reads
}

// Delete deletes an object from both stores; it fails as not found only when neither holds it.
func (m *moving) Delete(ctx context.Context, key string, opts *driver.DeleteOptions) error {
	if err := before(opts.BeforeDelete); err != nil {
		return err
	}
	toErr, fromErr := m.to.Delete(ctx, key), m.from.Delete(ctx, key)
	switch {
	case isNotFound(fromErr):
		fromErr = nil // the new store's answer stands, a not found included
	case isNotFound(toErr):
		toErr = nil // the old store held it
	}
	return errors.Join(toErr, fromErr)
}

// SignedURL signs a URL with the store that holds the object.
func (m *moving) SignedURL(ctx context.Context, key string, opts *driver.SignedURLOptions) (string, error) {
	store := m.to
	if ok, err := m.to.Exists(ctx, key); err != nil {
		return "", err //nolint:wrapcheck // the store's own error, whose code Go Cloud reads
	} else if !ok {
		store = m.from
	}
	return store.SignedURL(ctx, key, &blob.SignedURLOptions{ //nolint:wrapcheck // the store's own error, whose code Go Cloud reads
		Expiry: opts.Expiry, Method: opts.Method, ContentType: opts.ContentType,
		EnforceAbsentContentType: opts.EnforceAbsentContentType,
	})
}

// Close closes neither store: their opener does.
func (m *moving) Close() error { return nil }

// copyObject copies the object src names in from to dst in to, checked: its size against the old store's, and its MD5
// against the old store's when it keeps one and the new store's when it does. A copy that fails the check leaves no
// object in to.
func copyObject(ctx context.Context, to *blob.Bucket, dst string, from *blob.Bucket, src string) error {
	a, err := from.Attributes(ctx, src)
	if err != nil {
		return err //nolint:wrapcheck // the store's own error, whose code Go Cloud reads
	}
	r, err := from.NewReader(ctx, src, nil)
	if err != nil {
		return err //nolint:wrapcheck // the store's own error, whose code Go Cloud reads
	}
	defer func() { _ = r.Close() }() // only read
	write, cancel := context.WithCancel(ctx)
	defer cancel()
	w, err := to.NewWriter(write, dst, &blob.WriterOptions{
		CacheControl: a.CacheControl, ContentDisposition: a.ContentDisposition, ContentEncoding: a.ContentEncoding,
		ContentLanguage: a.ContentLanguage, ContentType: a.ContentType, DisableContentTypeDetection: true,
		ContentMD5: a.MD5, Metadata: a.Metadata,
	})
	if err != nil {
		return err //nolint:wrapcheck // the store's own error, whose code Go Cloud reads
	}
	sum := md5.New() //nolint:gosec // see the import
	n, err := io.Copy(w, io.TeeReader(r, sum))
	if err == nil && n != a.Size {
		err = fmt.Errorf("read %d bytes of %s, whose size is %d", n, src, a.Size)
	}
	if err != nil {
		cancel()      // so that Close writes nothing
		_ = w.Close() // the copy failed already
		return err    //nolint:wrapcheck // the store's own error, whose code Go Cloud reads
	}
	if err := w.Close(); err != nil {
		return err //nolint:wrapcheck // the store's own error, whose code Go Cloud reads
	}
	return checkCopy(ctx, to, dst, a.Size, sum.Sum(nil))
}

// checkCopy checks the copy dst against the size and MD5 its bytes had, and deletes it when it differs.
func checkCopy(ctx context.Context, to *blob.Bucket, dst string, size int64, sum []byte) error {
	got, err := to.Attributes(ctx, dst)
	if err != nil {
		return err //nolint:wrapcheck // the store's own error, whose code Go Cloud reads
	}
	if got.Size == size && (len(got.MD5) == 0 || bytes.Equal(got.MD5, sum)) {
		return nil
	}
	err = fmt.Errorf("the copy of %s is %d bytes with MD5 %x, not %d bytes with MD5 %x", dst, got.Size, got.MD5, size, sum)
	if delErr := to.Delete(ctx, dst); delErr != nil {
		return errors.Join(err, delErr)
	}
	return err
}

// Move copies every object the old store from holds and the new store to lacks into to, each checked by its size and
// MD5, past the others' failures, and counts what it copied and what is left. An object both hold with different
// sizes is left as it is and counted as left: objects never change, so the stores disagree, and the operator decides.
// It deletes nothing from either store.
func Move(ctx context.Context, to, from *blob.Bucket) (Moved, error) {
	var moved Moved
	var errs []error
	it := from.List(nil)
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			return moved, errors.Join(errs...)
		}
		if err != nil {
			return moved, errors.Join(append(errs, fmt.Errorf("listing the old store: %w", err))...)
		}
		copied, err := moveObject(ctx, to, from, obj)
		switch {
		case err != nil:
			moved.Left++
			errs = append(errs, fmt.Errorf("moving %s: %w", obj.Key, err))
		case copied:
			moved.Copied++
		}
	}
}

// moveObject copies obj from from into to unless to holds it already, and reports whether it copied it.
func moveObject(ctx context.Context, to, from *blob.Bucket, obj *blob.ListObject) (bool, error) {
	a, err := to.Attributes(ctx, obj.Key)
	switch {
	case isNotFound(err):
		return true, copyObject(ctx, to, obj.Key, from, obj.Key)
	case err != nil:
		return false, err //nolint:wrapcheck // the store's own error, which the caller wraps with the key
	case a.Size != obj.Size:
		return false, fmt.Errorf("the new store holds it with %d bytes, the old one with %d", a.Size, obj.Size)
	default:
		return false, nil
	}
}

// listToken is where a moving store's listing stands: each store's page token at the start of the page it was last
// reading, or done when it has none left, and the last key it returned, past which the next page starts.
type listToken struct {
	To       []byte `json:"to,omitempty"`
	ToDone   bool   `json:"toDone,omitempty"`
	From     []byte `json:"from,omitempty"`
	FromDone bool   `json:"fromDone,omitempty"`
	After    string `json:"after,omitempty"`
}

// storePage is a page of one store's listing, and the token of the page after it, empty after its last.
type storePage struct {
	objs []*blob.ListObject
	next []byte
}

// ListPaged lists both stores' objects as one listing, in key order, an object both hold once, as the new store's.
// Each page reads a page of each store from where it last stood and merges them, up to the end of the page that ends
// first, since the other store's next page may hold keys before the end of its own; a store's page that the merge used
// up moves its token on, and one it didn't is read again next time, past the last key returned.
func (m *moving) ListPaged(ctx context.Context, opts *driver.ListOptions) (*driver.ListPage, error) {
	if err := before(opts.BeforeList); err != nil {
		return nil, err
	}
	at := listToken{}
	if len(opts.PageToken) > 0 {
		if err := json.Unmarshal(opts.PageToken, &at); err != nil {
			return nil, fmt.Errorf("%w: %w", errBadToken, err)
		}
	}
	size := cmp.Or(opts.PageSize, listPage)
	list := &blob.ListOptions{Prefix: opts.Prefix, Delimiter: opts.Delimiter}
	for {
		to, err := page(ctx, m.to, at.To, at.ToDone, size, list)
		if err != nil {
			return nil, err
		}
		from, err := page(ctx, m.from, at.From, at.FromDone, size, list)
		if err != nil {
			return nil, err
		}
		objs := merge(to.objs, from.objs, at.After, bound(to, from), size)
		next := at
		if len(objs) > 0 {
			next.After = objs[len(objs)-1].Key
		}
		next.To, next.ToDone = advance(at.To, at.ToDone, to, next.After)
		next.From, next.FromDone = advance(at.From, at.FromDone, from, next.After)
		if next.ToDone && next.FromDone {
			return &driver.ListPage{Objects: objs}, nil
		}
		if len(objs) > 0 {
			token, err := json.Marshal(next)
			if err != nil {
				return nil, fmt.Errorf("writing the page token: %w", err)
			}
			return &driver.ListPage{Objects: objs, NextPageToken: token}, nil
		}
		at = next
	}
}

// page reads a page of store's listing at token, nothing when the store is done.
func page(ctx context.Context, store *blob.Bucket, token []byte, done bool, size int, list *blob.ListOptions) (storePage, error) {
	if done {
		return storePage{}, nil
	}
	if len(token) == 0 {
		token = blob.FirstPageToken
	}
	objs, next, err := store.ListPage(ctx, token, size, list)
	if err != nil {
		return storePage{}, err //nolint:wrapcheck // the store's own error, whose code Go Cloud reads
	}
	return storePage{objs: objs, next: next}, nil
}

// bound returns the last key a merge of two stores' pages may return: the end of the page that ends first among those
// with more pages after them, or nil when neither has. A store whose page is empty but not its last bounds the merge
// to nothing, until the next round reads its next page.
func bound(pages ...storePage) *string {
	var last *string
	for _, p := range pages {
		if len(p.next) == 0 {
			continue
		}
		if len(p.objs) == 0 {
			empty := ""
			return &empty
		}
		if key := p.objs[len(p.objs)-1].Key; last == nil || key < *last {
			last = &key
		}
	}
	return last
}

// advance returns where a store's listing stands once the keys up to last are returned: still at token while its page
// holds keys past last, otherwise at the next page, or done when there is none.
func advance(token []byte, done bool, p storePage, last string) ([]byte, bool) {
	if done {
		return nil, true
	}
	if len(p.objs) > 0 && p.objs[len(p.objs)-1].Key > last {
		return token, false
	}
	return p.next, len(p.next) == 0
}

// merge merges two pages of listings in key order into at most size objects past after and up to bound, when there is
// one, the new store's when both hold a key.
func merge(to, from []*blob.ListObject, after string, bound *string, size int) []*driver.ListObject {
	var objs []*driver.ListObject
	for len(objs) < size && (len(to) > 0 || len(from) > 0) {
		var obj *blob.ListObject
		switch {
		case len(from) == 0 || (len(to) > 0 && to[0].Key <= from[0].Key):
			obj = to[0]
			if len(from) > 0 && from[0].Key == obj.Key {
				from = from[1:]
			}
			to = to[1:]
		default:
			obj = from[0]
			from = from[1:]
		}
		if bound != nil && obj.Key > *bound {
			break
		}
		if obj.Key <= after {
			continue
		}
		objs = append(objs, &driver.ListObject{Key: obj.Key, ModTime: obj.ModTime, Size: obj.Size, MD5: obj.MD5, IsDir: obj.IsDir, AsFunc: exposeNothing})
	}
	return slices.Clip(objs)
}
