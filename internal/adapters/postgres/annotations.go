package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"cobranch.dev/internal/adapters/postgres/queries"
	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/repos"
)

// Annotations lists the annotations on one of the repository's commits by name, or repos.ErrNotFound when it holds no
// such commit (code.Store).
func (s *CodeStore) Annotations(ctx context.Context, repoID uuid.UUID, sha string) ([]code.Annotation, error) {
	if _, err := s.q.CommitOrg(ctx, repoID, sha); errors.Is(err, pgx.ErrNoRows) {
		return nil, repos.ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("finding commit %s: %w", sha, err)
	}
	byCommit, err := s.annotationsOf(ctx, repoID, []string{sha})
	if err != nil {
		return nil, err
	}
	return byCommit[sha], nil
}

// annotationsOf lists the annotations on those of the repository's commits that shas name, by commit, each commit's
// by name.
func (s *CodeStore) annotationsOf(ctx context.Context, repoID uuid.UUID, shas []string) (map[string][]code.Annotation, error) {
	if len(shas) == 0 {
		return nil, nil
	}
	rows, err := s.q.AnnotationsOf(ctx, repoID, shas)
	if err != nil {
		return nil, fmt.Errorf("reading the annotations: %w", err)
	}
	out := map[string][]code.Annotation{}
	for _, r := range rows {
		out[r.Annotation.Sha] = append(out[r.Annotation.Sha], annotationOf(r.Annotation, r.PrincipalDisplayName))
	}
	return out, nil
}

// SetAnnotation sets the annotation on the repository's commit for actor, or changes the one actor set there before,
// in one transaction that acts for actor: it appends event annotation.changed unless it changes nothing (code.Store).
func (s *CodeStore) SetAnnotation(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, sha string, in code.AnnotationInput) (code.Annotation, error) {
	var out code.Annotation
	err := transact(ctx, s.pool, s.q, "setting the annotation", actor, func(ctx context.Context, q *queries.Queries) error {
		orgID, err := q.CommitOrg(ctx, repoID, sha)
		if errors.Is(err, pgx.ErrNoRows) {
			return repos.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("finding commit %s: %w", sha, err)
		}
		row, created, err := lockAnnotation(ctx, q, actor, orgID, repoID, sha, in)
		out = row
		switch {
		case err != nil || created:
			return err
		case row.Principal == nil || row.Principal.ID != actor:
			return &code.TakenError{Name: in.Name, By: row.Principal}
		case in.Version == code.NoAnnotation, code.Stale(in.Version, row.Version):
			return code.ErrAnnotationChanged
		case row.Status == in.Status && row.Summary == in.Summary && row.Link == in.Link:
			return nil
		}
		updated, err := q.UpdateAnnotation(ctx, queries.UpdateAnnotationParams{Status: string(in.Status), Summary: in.Summary, Link: in.Link, ID: row.ID})
		if err != nil {
			return fmt.Errorf("changing annotation %s: %w", in.Name, err)
		}
		out = annotationOf(updated, &row.Principal.DisplayName)
		return annotationEvent(ctx, q, orgID, actor, "set", repoID, out)
	})
	return out, err
}

// lockAnnotation answers the annotation in.Name on the commit, locked until the transaction ends, and false; or, when
// there is none and in names no version or code.NoAnnotation, sets it for actor, appends its event and answers it and
// true. Its insert locks the annotation there instead of setting one, waiting for a transaction that sets or deletes
// it, so it answers one whatever runs beside it. A version named of one that isn't there is code.ErrAnnotationChanged.
func lockAnnotation(ctx context.Context, q *queries.Queries, actor, orgID, repoID uuid.UUID, sha string, in code.AnnotationInput) (code.Annotation, bool, error) {
	created := false
	if in.Version == 0 || in.Version == code.NoAnnotation {
		id, err := uuid.NewV7()
		if err != nil {
			return code.Annotation{}, false, fmt.Errorf("naming the annotation: %w", err)
		}
		_, err = q.InsertAnnotation(ctx, queries.InsertAnnotationParams{ID: id, OrgID: orgID, RepoID: repoID, Sha: sha, Name: in.Name,
			Status: string(in.Status), Summary: in.Summary, Link: in.Link, CreatedBy: &actor})
		created = err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) { // no row: one is there, which the insert locked
			return code.Annotation{}, false, fmt.Errorf("setting annotation %s: %w", in.Name, err)
		}
	}
	row, err := q.AnnotationForUpdate(ctx, repoID, sha, in.Name)
	if errors.Is(err, pgx.ErrNoRows) && in.Version != 0 && in.Version != code.NoAnnotation {
		return code.Annotation{}, false, code.ErrAnnotationChanged
	}
	if err != nil {
		return code.Annotation{}, false, fmt.Errorf("finding annotation %s: %w", in.Name, err)
	}
	a := annotationOf(row.Annotation, row.PrincipalDisplayName)
	if !created {
		return a, false, nil
	}
	return a, true, annotationEvent(ctx, q, orgID, actor, "set", repoID, a)
}

// DeleteAnnotation deletes the annotation of name from the repository's commit, when actor set it or anyone is true
// and it is at the version that version names, in one transaction that acts for actor, and appends event
// annotation.changed (code.Store). One that isn't there, on a commit the repository holds, is
// code.ErrAnnotationChanged when version names one.
func (s *CodeStore) DeleteAnnotation(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, sha, name string, version int64, anyone bool) error {
	return transact(ctx, s.pool, s.q, "deleting the annotation", actor, func(ctx context.Context, q *queries.Queries) error {
		row, err := q.AnnotationForUpdate(ctx, repoID, sha, name)
		if errors.Is(err, pgx.ErrNoRows) {
			return missingAnnotation(ctx, q, repoID, sha, version)
		}
		if err != nil {
			return fmt.Errorf("finding annotation %s: %w", name, err)
		}
		a := annotationOf(row.Annotation, row.PrincipalDisplayName)
		if !anyone && (a.Principal == nil || a.Principal.ID != actor) {
			return &code.TakenError{Name: name, By: a.Principal}
		}
		if code.Stale(version, a.Version) {
			return code.ErrAnnotationChanged
		}
		if err := q.DeleteAnnotation(ctx, a.ID); err != nil {
			return fmt.Errorf("deleting annotation %s: %w", name, err)
		}
		return annotationEvent(ctx, q, row.Annotation.OrgID, actor, "deleted", repoID, a)
	})
}

// missingAnnotation is why an annotation the commit doesn't hold can't be deleted: code.ErrAnnotationChanged when the
// deletion names a version of it, on a commit the repository holds; repos.ErrNotFound otherwise.
func missingAnnotation(ctx context.Context, q *queries.Queries, repoID uuid.UUID, sha string, version int64) error {
	if version == 0 {
		return repos.ErrNotFound
	}
	if _, err := q.CommitOrg(ctx, repoID, sha); errors.Is(err, pgx.ErrNoRows) {
		return repos.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("finding commit %s: %w", sha, err)
	}
	return code.ErrAnnotationChanged
}

// annotationEvent appends event annotation.changed about annotation a on the repository, as actor, saying whether a
// was set (or changed) or deleted.
func annotationEvent(ctx context.Context, q *queries.Queries, orgID, actor uuid.UUID, action string, repoID uuid.UUID, a code.Annotation) error {
	details := map[string]any{"repo": repoID, "name": a.Name, "action": action, "status": a.Status, "version": a.Version}
	if a.Principal != nil && a.Principal.ID != actor {
		details["setBy"] = a.Principal.ID
	}
	return orgEvent(ctx, q, &orgID, actor, "annotation.changed", a.ID, details)
}

// annotationOf is the domain's view of an annotations row, with the display name of the principal who set it.
func annotationOf(a queries.Annotation, displayName *string) code.Annotation {
	return code.Annotation{ID: a.ID, Name: a.Name, Status: code.AnnotationStatus(a.Status), Summary: a.Summary, Link: a.Link,
		Principal: principalOf(a.CreatedBy, displayName), CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, StartedAt: a.StartedAt,
		CompletedAt: a.CompletedAt, Version: a.Version}
}
