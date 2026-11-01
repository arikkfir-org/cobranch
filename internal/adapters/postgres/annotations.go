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
// in one transaction that acts for actor: it appends event annotation.set unless it changes nothing (code.Store).
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
		row, created, err := s.insertAnnotation(ctx, q, actor, orgID, repoID, sha, in)
		if err != nil || created {
			out = row
			return err
		}
		if row.Principal == nil || row.Principal.ID != actor {
			return &code.TakenError{Name: in.Name, By: row.Principal}
		}
		if row.Status == in.Status && row.Summary == in.Summary && row.Link == in.Link {
			out = row
			return nil
		}
		updated, err := q.UpdateAnnotation(ctx, queries.UpdateAnnotationParams{Status: string(in.Status), Summary: in.Summary, Link: in.Link, ID: row.ID})
		if err != nil {
			return fmt.Errorf("changing annotation %s: %w", in.Name, err)
		}
		out = annotationOf(updated, &row.Principal.DisplayName)
		return annotationEvent(ctx, q, orgID, actor, "annotation.set", repoID, out)
	})
	return out, err
}

// insertAnnotation sets the annotation unless it is there, and appends its event, answering it and true; or answers
// the one there, locked until the transaction ends, and false.
func (s *CodeStore) insertAnnotation(ctx context.Context, q *queries.Queries, actor, orgID, repoID uuid.UUID, sha string, in code.AnnotationInput) (code.Annotation, bool, error) {
	if row, err := q.AnnotationForUpdate(ctx, repoID, sha, in.Name); err == nil {
		return annotationOf(row.Annotation, row.PrincipalDisplayName), false, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return code.Annotation{}, false, fmt.Errorf("finding annotation %s: %w", in.Name, err)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return code.Annotation{}, false, fmt.Errorf("naming the annotation: %w", err)
	}
	_, err = q.InsertAnnotation(ctx, queries.InsertAnnotationParams{ID: id, OrgID: orgID, RepoID: repoID, Sha: sha, Name: in.Name,
		Status: string(in.Status), Summary: in.Summary, Link: in.Link, CreatedBy: &actor})
	created := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) { // no row: another transaction set it first, and it is there now
		return code.Annotation{}, false, fmt.Errorf("setting annotation %s: %w", in.Name, err)
	}
	row, err := q.AnnotationForUpdate(ctx, repoID, sha, in.Name)
	if err != nil {
		return code.Annotation{}, false, fmt.Errorf("finding annotation %s: %w", in.Name, err)
	}
	a := annotationOf(row.Annotation, row.PrincipalDisplayName)
	if !created {
		return a, false, nil
	}
	return a, true, annotationEvent(ctx, q, orgID, actor, "annotation.set", repoID, a)
}

// DeleteAnnotation deletes the annotation of name from the repository's commit, when actor set it or anyone is true,
// in one transaction that acts for actor, and appends event annotation.deleted (code.Store).
func (s *CodeStore) DeleteAnnotation(ctx context.Context, actor uuid.UUID, repoID uuid.UUID, sha, name string, anyone bool) error {
	return transact(ctx, s.pool, s.q, "deleting the annotation", actor, func(ctx context.Context, q *queries.Queries) error {
		row, err := q.AnnotationForUpdate(ctx, repoID, sha, name)
		if errors.Is(err, pgx.ErrNoRows) {
			return repos.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("finding annotation %s: %w", name, err)
		}
		a := annotationOf(row.Annotation, row.PrincipalDisplayName)
		if !anyone && (a.Principal == nil || a.Principal.ID != actor) {
			return &code.TakenError{Name: name, By: a.Principal}
		}
		if err := q.DeleteAnnotation(ctx, a.ID); err != nil {
			return fmt.Errorf("deleting annotation %s: %w", name, err)
		}
		return annotationEvent(ctx, q, row.Annotation.OrgID, actor, "annotation.deleted", repoID, a)
	})
}

// annotationEvent appends event kind about annotation a on the repository, as actor.
func annotationEvent(ctx context.Context, q *queries.Queries, orgID, actor uuid.UUID, kind string, repoID uuid.UUID, a code.Annotation) error {
	details := map[string]any{"repo": repoID, "name": a.Name, "status": a.Status}
	if a.Principal != nil && a.Principal.ID != actor {
		details["setBy"] = a.Principal.ID
	}
	return orgEvent(ctx, q, &orgID, actor, kind, a.ID, details)
}

// annotationOf is the domain's view of an annotations row, with the display name of the principal who set it.
func annotationOf(a queries.Annotation, displayName *string) code.Annotation {
	return code.Annotation{ID: a.ID, Name: a.Name, Status: code.AnnotationStatus(a.Status), Summary: a.Summary, Link: a.Link,
		Principal: principalOf(a.CreatedBy, displayName), CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, StartedAt: a.StartedAt,
		CompletedAt: a.CompletedAt}
}
