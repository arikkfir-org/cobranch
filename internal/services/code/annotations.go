package code

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"cobranch.dev/internal/services/auth"
	"cobranch.dev/internal/services/orgs"
)

// AnnotationStatus is where an annotation stands: Pending or In progress while its principal works on it, then Failed
// or Successful, in any order its principal sets them.
type AnnotationStatus string

// The four statuses of an annotation.
const (
	AnnotationPending    AnnotationStatus = "pending"
	AnnotationInProgress AnnotationStatus = "in_progress"
	AnnotationFailed     AnnotationStatus = "failed"
	AnnotationSuccessful AnnotationStatus = "successful"
)

// AnnotationStatuses lists the statuses in the order their principal usually moves through them.
var AnnotationStatuses = []AnnotationStatus{AnnotationPending, AnnotationInProgress, AnnotationFailed, AnnotationSuccessful}

// Done reports whether the status ends a run: Failed or Successful.
func (s AnnotationStatus) Done() bool {
	return s == AnnotationFailed || s == AnnotationSuccessful
}

// The permissions annotations take.
const (
	// PermAnnotate sets annotations, and changes and deletes the viewer's own.
	PermAnnotate = "annotations.write"
	// PermDeleteAnyAnnotation deletes any principal's annotations.
	PermDeleteAnyAnnotation = "annotations.delete_any"
)

// Annotation is a principal's verdict on a commit, such as ci/build passing: it belongs to the commit, so every branch
// at that commit shows it. A commit holds at most one of each name, which only its principal changes.
type Annotation struct {
	ID      uuid.UUID
	Name    string
	Status  AnnotationStatus
	Summary string
	// Link is where its details are, an absolute http or https URL; empty without one.
	Link string
	// Principal is who set it; nil once they are gone.
	Principal *Principal
	CreatedAt time.Time
	UpdatedAt time.Time
	// StartedAt is when its run started: when it was set, or when it last went back from Failed or Successful to
	// Pending or In progress.
	StartedAt time.Time
	// CompletedAt is when it reached Failed or Successful; nil while it runs.
	CompletedAt *time.Time
}

// AnnotationInput is what a principal sets an annotation to.
type AnnotationInput struct {
	Name    string
	Status  AnnotationStatus
	Summary string
	Link    string
}

// MaxAnnotationSummary is the longest summary an annotation takes, in characters.
const MaxAnnotationSummary = 500

// maxAnnotationLink is the longest link an annotation takes, in bytes.
const maxAnnotationLink = 2048

// ErrAnnotationTaken is an annotation another principal set: only they change it, and only they or a principal
// holding annotations.delete_any delete it. A *TakenError, which names them, matches it.
var ErrAnnotationTaken = errors.New("another principal set this annotation")

// TakenError refuses a change to an annotation that another principal set, naming them.
type TakenError struct {
	Name string
	// By is who set it; nil once they are gone.
	By *Principal
}

func (e *TakenError) Error() string {
	by := "A principal since gone"
	if e.By != nil {
		by = e.By.DisplayName
	}
	return fmt.Sprintf("%s set annotation %s on this commit; only they change it.", by, e.Name)
}

// Is matches ErrAnnotationTaken.
func (e *TakenError) Is(target error) bool { return target == ErrAnnotationTaken }

// annotationName is an annotation's name: 1 to 100 lowercase letters, digits, '-', '_', '.' and '/', starting with a
// letter or digit.
var annotationName = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,99}$`)

// ValidateAnnotationName reports whether name names an annotation.
func ValidateAnnotationName(name string) error {
	if !annotationName.MatchString(name) {
		return &orgs.ValidationError{Field: "name", Code: "name.invalid",
			Message: "an annotation's name: 1 to 100 lowercase letters, digits, '-', '_', '.' and '/', starting with a letter or digit"}
	}
	return nil
}

// ValidateAnnotation reports whether in sets an annotation: its name, one of the four statuses, a summary of at most
// MaxAnnotationSummary characters, and a link that is empty or an absolute http or https URL.
func ValidateAnnotation(in AnnotationInput) error {
	if err := ValidateAnnotationName(in.Name); err != nil {
		return err
	}
	if !slices.Contains(AnnotationStatuses, in.Status) {
		return &orgs.ValidationError{Field: "status", Code: "status.invalid", Message: "pending, in_progress, failed or successful"}
	}
	if !utf8.ValidString(in.Summary) || utf8.RuneCountInString(in.Summary) > MaxAnnotationSummary {
		return &orgs.ValidationError{Field: "summary", Code: "summary.invalid", Message: "at most 500 characters"}
	}
	if in.Link == "" {
		return nil
	}
	u, err := url.Parse(in.Link)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || len(in.Link) > maxAnnotationLink {
		return &orgs.ValidationError{Field: "link", Code: "link.invalid", Message: "an absolute http or https URL of at most 2048 bytes"}
	}
	return nil
}

// Annotations lists the annotations on one of the repository's commits, by name; it takes repo.read. A commit the
// repository doesn't hold is repos.ErrNotFound.
func (s *Service) Annotations(ctx context.Context, v Viewer, repoID uuid.UUID, sha string) ([]Annotation, error) {
	ctx, span := tracer.Start(ctx, "code.Service.Annotations")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.commit.sha", sha))
	if err := s.read(ctx, v, repoID); err != nil {
		return nil, failed(span, "reading the annotations", err)
	}
	if err := ValidateSHA("sha", sha); err != nil {
		return nil, err
	}
	list, err := s.store.Annotations(ctx, repoID, sha)
	if err != nil {
		return nil, failed(span, "reading the annotations", err)
	}
	return list, nil
}

// SetAnnotation sets an annotation on one of the repository's commits for v, or changes the one v set there before,
// and answers it as it now is; it takes annotations.write. An annotation another principal set is a *TakenError, and
// a commit the repository doesn't hold is repos.ErrNotFound.
func (s *Service) SetAnnotation(ctx context.Context, v Viewer, repoID uuid.UUID, sha string, in AnnotationInput) (Annotation, error) {
	ctx, span := tracer.Start(ctx, "code.Service.SetAnnotation")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.commit.sha", sha),
		attribute.String("cobranch.annotation.name", in.Name), attribute.String("cobranch.annotation.status", string(in.Status)))
	held, err := s.holds(ctx, v, repoID)
	if err != nil {
		return Annotation{}, failed(span, "setting the annotation", err)
	}
	if !slices.Contains(held, PermAnnotate) {
		return Annotation{}, fmt.Errorf("%w: %s on the repository", auth.ErrForbidden, PermAnnotate)
	}
	if err := ValidateSHA("sha", sha); err != nil {
		return Annotation{}, err
	}
	if err := ValidateAnnotation(in); err != nil {
		return Annotation{}, err
	}
	a, err := s.store.SetAnnotation(ctx, v.ID, repoID, sha, in)
	if err != nil {
		return Annotation{}, failed(span, "setting the annotation", err)
	}
	return a, nil
}

// DeleteAnnotation deletes an annotation from one of the repository's commits, which frees its name there: v's own
// with annotations.write, any principal's with annotations.delete_any. One that isn't there is repos.ErrNotFound.
func (s *Service) DeleteAnnotation(ctx context.Context, v Viewer, repoID uuid.UUID, sha, name string) error {
	ctx, span := tracer.Start(ctx, "code.Service.DeleteAnnotation")
	defer span.End()
	span.SetAttributes(attribute.String("cobranch.repository.id", repoID.String()), attribute.String("cobranch.commit.sha", sha),
		attribute.String("cobranch.annotation.name", name))
	held, err := s.holds(ctx, v, repoID)
	if err != nil {
		return failed(span, "deleting the annotation", err)
	}
	anyone := slices.Contains(held, PermDeleteAnyAnnotation)
	if !anyone && !slices.Contains(held, PermAnnotate) {
		return fmt.Errorf("%w: %s on the repository", auth.ErrForbidden, PermAnnotate)
	}
	if err := ValidateSHA("sha", sha); err != nil {
		return err
	}
	if err := ValidateAnnotationName(name); err != nil {
		return err
	}
	err = s.store.DeleteAnnotation(ctx, v.ID, repoID, sha, name, anyone)
	if errors.Is(err, ErrAnnotationTaken) {
		return fmt.Errorf("%w: %s on the repository, to delete another principal's annotation", auth.ErrForbidden, PermDeleteAnyAnnotation)
	}
	return failed(span, "deleting the annotation", err)
}
