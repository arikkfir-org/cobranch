package rest

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"cobranch.dev/internal/services/auth"
)

// preferencesOf is p as the API shows them.
func preferencesOf(p auth.Preferences) Preferences {
	return Preferences{Theme: Theme(p.Theme), Version: p.Version}
}

// preferencesTag is the preferences' ETag: their version, quoted, "0" before the first change.
func preferencesTag(p auth.Preferences) string {
	return `"` + strconv.FormatInt(p.Version, 10) + `"`
}

// preferencesMatch is the version of the preferences that If-Match names: 428 without one, auth.AnyPreferences for *,
// and 412 for a tag that names no version, a weak one included, since If-Match compares strongly. Unlike other
// resources' tags, "0" names one: the defaults, before the first change.
func preferencesMatch(header *string) (int64, error) {
	if header == nil || strings.TrimSpace(*header) == "" {
		return 0, refuse(http.StatusPreconditionRequired, "precondition.required", `Name the preferences' ETag in If-Match, "0" before the first change, or *.`)
	}
	tag := strings.TrimSpace(*header)
	if tag == "*" {
		return auth.AnyPreferences, nil
	}
	version, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(tag, `"`), `"`), 10, 64)
	if err != nil || version < 0 || !strings.HasPrefix(tag, `"`) {
		return 0, refuse(http.StatusPreconditionFailed, "precondition.failed", "If-Match names no version of the preferences.")
	}
	return version, nil
}

// failPreferences maps the preferences' answers, then fail's.
func failPreferences(err error) error {
	switch {
	case errors.Is(err, auth.ErrForbidden):
		return refuse(http.StatusForbidden, "preferences.people_only", "Only a person has preferences, never an agent, a job or an API key.")
	case errors.Is(err, auth.ErrPreferencesChanged):
		return refuse(http.StatusPreconditionFailed, "precondition.failed", "Your preferences changed since you read them: read them again.")
	}
	return fail(err, "preferences")
}

// GetMyPreferences answers the viewer's preferences, or 304 while the caller holds them.
func (a api) GetMyPreferences(ctx context.Context, req GetMyPreferencesRequestObject) (GetMyPreferencesResponseObject, error) {
	v, ok := auth.Viewer(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	p, err := a.sessions.Preferences(ctx, v)
	if err != nil {
		return nil, failPreferences(err)
	}
	tag := preferencesTag(p)
	if matches(req.Params.IfNoneMatch, tag) {
		return GetMyPreferences304Response{Headers: GetMyPreferences304ResponseHeaders{ETag: tag}}, nil
	}
	return GetMyPreferences200JSONResponse{Body: preferencesOf(p), Headers: GetMyPreferences200ResponseHeaders{ETag: tag}}, nil
}

// UpdateMyPreferences changes the viewer's preferences the merge patch names.
func (a api) UpdateMyPreferences(ctx context.Context, req UpdateMyPreferencesRequestObject) (UpdateMyPreferencesResponseObject, error) {
	v, ok := auth.Viewer(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	version, err := preferencesMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	patch := auth.PreferencesPatch{Version: version}
	if req.Body.Theme != nil {
		t := auth.Theme(*req.Body.Theme)
		patch.Theme = &t
	}
	p, err := a.sessions.UpdatePreferences(ctx, v, patch)
	if err != nil {
		return nil, failPreferences(err)
	}
	return UpdateMyPreferences200JSONResponse{Body: preferencesOf(p), Headers: UpdateMyPreferences200ResponseHeaders{ETag: preferencesTag(p)}}, nil
}
