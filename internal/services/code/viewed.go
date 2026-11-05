package code

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// The bounds of a person's Viewed marks on a branch: one change sets or takes off at most as many marks as a diff
// lists files, and a branch keeps at most MaxViewedFiles of them per person.
const (
	MaxViewedChanges = MaxDiffFiles
	MaxViewedFiles   = 10000
)

// AnyViewed is the tag of a change to whichever Viewed marks are there.
const AnyViewed = "*"

// ErrViewedChanged answers a change to Viewed marks that changed since the tag it names.
var ErrViewedChanged = errors.New("the Viewed marks changed since they were read")

// ViewedFile is a person's mark on a file of a branch, which its Files changed tab shows as Viewed: the file's path,
// the object of its content when they marked it, "" for a file the branch deletes, and when they did. A file whose
// object has moved on since shows unviewed again.
type ViewedFile struct {
	Path     string
	Object   string
	ViewedAt time.Time
}

// ViewedChange marks the file at Path Viewed at Object, "" for a file the branch deletes, or, when Unmark, takes its
// mark off.
type ViewedChange struct {
	Path   string
	Object string
	Unmark bool
}

// ViewedStore keeps people's Viewed marks on branches.
type ViewedStore interface {
	// ViewedFiles lists actor's marks on the repository's live branch of name, by path: repos.ErrNotFound when it has
	// no such branch.
	ViewedFiles(ctx context.Context, actor, repoID uuid.UUID, branch string) ([]ViewedFile, error)
	// UpdateViewed applies changes, at at, to actor's marks on the repository's live branch of name, once check
	// passes on the marks as they are, all in one transaction that names actor, and answers the marks as they then
	// are: repos.ErrNotFound when it has no such branch, check's error as it returns it, and a *orgs.ValidationError
	// when actor would keep more than MaxViewedFiles.
	UpdateViewed(ctx context.Context, actor, repoID uuid.UUID, branch string, check func([]ViewedFile) error, changes []ViewedChange,
		at time.Time) ([]ViewedFile, error)
}

// ViewedTag is the tag of marks, their ETag: a digest of each one's path, object and moment, in order.
func ViewedTag(marks []ViewedFile) string {
	h := sha256.New()
	for _, m := range marks {
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00%d\x00", m.Path, m.Object, m.ViewedAt.UnixMicro()) // a hash takes every write
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil)[:18])
}

// Viewed lists v's Viewed marks on the repository's branch, by path; it takes repo.read, and a signed-in viewer,
// since the marks are theirs.
func (s *Service) Viewed(ctx context.Context, v Viewer, repoID uuid.UUID, branch string) ([]ViewedFile, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Viewed")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.branch", branch))
	if err := s.readViewed(ctx, v, repoID); err != nil {
		return nil, failed(span, "reading the Viewed marks", err)
	}
	marks, err := s.store.ViewedFiles(ctx, v.ID, repoID, branch)
	if err != nil {
		return nil, failed(span, "reading the Viewed marks", err)
	}
	return marks, nil
}

// UpdateViewed applies changes to v's Viewed marks on the repository's branch while they are still those tag names
// (AnyViewed for whichever are there), and answers them as they then are; it takes repo.read, and a signed-in
// viewer. A tag that names other marks is ErrViewedChanged.
func (s *Service) UpdateViewed(ctx context.Context, v Viewer, repoID uuid.UUID, branch, tag string, changes []ViewedChange) ([]ViewedFile, error) {
	ctx, span := tracer.Start(ctx, "code.Service.UpdateViewed")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.branch", branch),
		attribute.Int("cobranch.viewed.changes", len(changes)))
	if err := s.readViewed(ctx, v, repoID); err != nil {
		return nil, failed(span, "changing the Viewed marks", err)
	}
	if err := validateViewed(changes); err != nil {
		return nil, err
	}
	check := func(marks []ViewedFile) error {
		if tag != AnyViewed && tag != ViewedTag(marks) {
			return ErrViewedChanged
		}
		return nil
	}
	marks, err := s.store.UpdateViewed(ctx, v.ID, repoID, branch, check, changes, time.Now().UTC())
	if err != nil {
		return nil, failed(span, "changing the Viewed marks", err)
	}
	return marks, nil
}

// readViewed checks that v reads the repository and is someone whose marks Cobranch keeps: not Anonymous.
func (s *Service) readViewed(ctx context.Context, v Viewer, repoID uuid.UUID) error {
	if err := s.read(ctx, v, repoID); err != nil {
		return err
	}
	if v.ID == auth.AnonymousID {
		return auth.ErrForbidden
	}
	return nil
}

// validateViewed checks changes: at most MaxViewedChanges, each a file's path once, with an object's full name or ""
// to mark it.
func validateViewed(changes []ViewedChange) error {
	if len(changes) > MaxViewedChanges {
		return &orgs.ValidationError{Field: "files", Code: "files.too_many", Message: fmt.Sprintf("at most %d files at once", MaxViewedChanges)}
	}
	seen := make(map[string]bool, len(changes))
	for _, c := range changes {
		if c.Path == "" || ValidatePath(c.Path) != nil {
			return &orgs.ValidationError{Field: "files", Code: "path.invalid", Message: "each a file's path: names separated by /, from the repository's root, without . or .."}
		}
		if seen[c.Path] {
			return &orgs.ValidationError{Field: "files", Code: "path.repeated", Message: "each file once: " + c.Path}
		}
		seen[c.Path] = true
		if !c.Unmark && c.Object != "" {
			if err := ValidateSHA("object", c.Object); err != nil {
				return err
			}
		}
	}
	return nil
}
