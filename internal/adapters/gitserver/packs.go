package gitserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"

	"cobranch.dev/internal/services/git"
)

// Packs is the bucket's packs for the maintenance job: it repacks a repository's packs into one with the git CLI, in
// a throwaway repository under its directory, lists the bucket's uploads and deletes them (git.Packs).
type Packs struct {
	dir    string
	git    string
	bucket *blob.Bucket
}

// NewPacks returns the bucket's packs, repacking under dir, which it makes, with the git CLI at bin ("git" finds it on
// the PATH).
func NewPacks(dir, bin string, bucket *blob.Bucket) (*Packs, error) {
	if bin == "" {
		bin = "git"
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("making %s: %w", dir, err)
	}
	return &Packs{dir: dir, git: bin, bucket: bucket}, nil
}

// repackConfig is a repacked repository's configuration: one delta search thread, so a repack's memory stays one
// window's, and no bitmap or reverse index, which the bucket doesn't keep.
const repackConfig = "[core]\n\trepositoryformatversion = 0\n\tbare = true\n[gc]\n\tauto = 0\n" +
	"[pack]\n\tthreads = 1\n\twriteReverseIndex = false\n[repack]\n\twriteBitmaps = false\n"

// Repack downloads the snapshot's packs into a throwaway repository with its refs, runs git repack -a -d
// --keep-unreachable, which keeps every object they hold, reachable or not, and uploads the pack it made (git.Packs).
// Unreachable objects stay because a push that started before the snapshot may build on a ref deleted since.
func (p *Packs) Repack(ctx context.Context, repoID uuid.UUID, snapshot git.Snapshot) (git.Pack, error) {
	dir, err := os.MkdirTemp(p.dir, "repack-")
	if err != nil {
		return git.Pack{}, fmt.Errorf("making a throwaway repository: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }() // the pod's end empties the directory anyway
	if err := layout(dir, snapshot, repackConfig); err != nil {
		return git.Pack{}, err
	}
	packDir := filepath.Join(dir, "objects", "pack")
	for _, pack := range snapshot.Packs {
		for _, suffix := range packFiles {
			if _, err := fetch(ctx, p.bucket, packKey(repoID, pack.Upload, pack.Name+suffix), filepath.Join(packDir, pack.Name+suffix)); err != nil {
				return git.Pack{}, err
			}
		}
	}
	cmd := gitCommand(ctx, p.git, p.dir, nil, "--git-dir="+dir, "repack", "-a", "-d", "--keep-unreachable", "-q")
	var stderr bytes.Buffer
	cmd.Stderr = &limited{buf: &stderr, n: 4096}
	if err := cmd.Run(); err != nil {
		return git.Pack{}, fmt.Errorf("git repack: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	found, err := filepath.Glob(filepath.Join(packDir, "pack-*.pack"))
	if err != nil || len(found) != 1 {
		return git.Pack{}, fmt.Errorf("git repack left %d packs (%v)", len(found), err)
	}
	name := strings.TrimSuffix(filepath.Base(found[0]), ".pack")
	pack, _, err := putPack(ctx, p.bucket, repoID, name, func(file string) string { return filepath.Join(packDir, file) })
	return pack, err
}

// Uploads lists every upload the bucket holds under git/, with when its last file was written (git.Packs). An object
// there of another shape is left alone.
func (p *Packs) Uploads(ctx context.Context) ([]git.Upload, error) {
	var uploads []git.Upload
	seen := map[uuid.UUID]int{}
	it := p.bucket.List(&blob.ListOptions{Prefix: packsPrefix})
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			return uploads, nil
		}
		if err != nil {
			return nil, fmt.Errorf("listing the bucket's packs: %w", err)
		}
		repoID, upload, ok := parsePackKey(obj.Key)
		if !ok {
			continue
		}
		if i, ok := seen[upload]; ok {
			if obj.ModTime.After(uploads[i].Written) {
				uploads[i].Written = obj.ModTime
			}
			continue
		}
		seen[upload] = len(uploads)
		uploads = append(uploads, git.Upload{RepoID: repoID, ID: upload, Written: obj.ModTime})
	}
}

// Delete deletes every file of a repository's upload; a file already gone is no error (git.Packs).
func (p *Packs) Delete(ctx context.Context, repoID, upload uuid.UUID) error {
	it := p.bucket.List(&blob.ListOptions{Prefix: packKey(repoID, upload, "")})
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("listing upload %s: %w", upload, err)
		}
		if err := p.bucket.Delete(ctx, obj.Key); err != nil && gcerrors.Code(err) != gcerrors.NotFound {
			return fmt.Errorf("deleting %s: %w", obj.Key, err)
		}
	}
}
