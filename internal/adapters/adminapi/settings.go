package adminapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/admin"
	"cobranch.dev/internal/services/auth"
)

// GetSettings answers the installation's settings with their ETag, or 304 while If-None-Match names it.
func (a api) GetSettings(ctx context.Context, req GetSettingsRequestObject) (GetSettingsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	s, err := a.admin.Settings(ctx, v)
	if err != nil {
		return nil, fail(err, "setting")
	}
	tag := etag(s)
	if req.Params.IfNoneMatch != nil && matches(*req.Params.IfNoneMatch, tag) {
		return GetSettings304Response{Headers: GetSettings304ResponseHeaders{ETag: tag}}, nil
	}
	return GetSettings200JSONResponse{Body: settings(s), Headers: GetSettings200ResponseHeaders{ETag: tag}}, nil
}

// ChangeSettings changes the settings the body names, from those If-Match names: 428 without it, 412 once they
// changed.
func (a api) ChangeSettings(ctx context.Context, req ChangeSettingsRequestObject) (ChangeSettingsResponseObject, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	match, err := ifMatch(req.Params.IfMatch, "the settings'")
	if err != nil {
		return nil, err
	}
	change := admin.SettingsChange{SignUpDomains: req.Body.SignUpDomains, SelfServe: req.Body.SelfServe}
	if req.Body.SignUp != nil {
		policy := auth.Policy(*req.Body.SignUp)
		change.SignUp = &policy
	}
	s, err := a.admin.ChangeSettings(ctx, v, match, change)
	switch {
	case errors.Is(err, admin.ErrStale):
		return nil, refuse(http.StatusPreconditionFailed, "precondition.failed", "The settings changed since you read them: read them again.")
	case errors.Is(err, admin.ErrLocked):
		return nil, refuse(http.StatusConflict, "setting.locked", "The chart's values set this setting: change it there.")
	case err != nil:
		return nil, fail(err, "setting")
	}
	return ChangeSettings200JSONResponse{Body: settings(s), Headers: ChangeSettings200ResponseHeaders{ETag: etag(s)}}, nil
}

// etag is the settings' strong entity tag.
func etag(s admin.Settings) string {
	return `"` + s.Fingerprint() + `"`
}

// opaque is an entity tag's opaque part, without its quotes or a weak prefix: If-None-Match compares weakly.
func opaque(tag string) string {
	return strings.Trim(strings.TrimPrefix(strings.TrimSpace(tag), "W/"), `"`)
}

// matches reports whether an If-None-Match header names tag: as one of its list, or as *.
func matches(header, tag string) bool {
	for t := range strings.SplitSeq(header, ",") {
		if t = strings.TrimSpace(t); t == "*" || opaque(t) == opaque(tag) {
			return true
		}
	}
	return false
}

func settings(s admin.Settings) Settings {
	out := Settings{SignUp: SignUpPolicy(s.SignUp), SignUpDomains: nonNil(s.SignUpDomains), SelfServe: s.SelfServe,
		Locked: make([]SettingsLocked, 0, len(s.Locked)), UpdatedAt: s.UpdatedAt}
	for _, l := range s.Locked {
		out.Locked = append(out.Locked, SettingsLocked(l))
	}
	if s.UpdatedBy != uuid.Nil {
		by := s.UpdatedBy
		out.UpdatedBy = &by
	}
	return out
}

// ifMatch reads If-Match's one strong tag, or refuses with 428 when there is none; whose names the resource.
func ifMatch(header *string, whose string) (string, error) {
	if header == nil || strings.TrimSpace(*header) == "" {
		return "", refuse(http.StatusPreconditionRequired, "precondition.required", "Name "+whose+" ETag in If-Match.")
	}
	// If-Match compares strongly: a weak tag matches nothing.
	return strings.Trim(strings.TrimSpace(*header), `"`), nil
}
