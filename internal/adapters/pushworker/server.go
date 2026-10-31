package pushworker

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"cobranch.dev/internal/services/git"
)

// Recorder records pushes: git.Service.
type Recorder interface {
	Record(ctx context.Context, push git.Push) (git.Answer, error)
}

// Handler serves the push worker's call, POST /v1/pushes, to callers that bear token: a push's manifest in, the
// answer out, once the push is committed or refused. A push it couldn't record answers 500, and the Git server asks
// again.
func Handler(token string, recorder Recorder) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+Path, func(w http.ResponseWriter, r *http.Request) {
		if !bears(r, token) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="cobranch-internal"`)
			http.Error(w, "only the Git server calls the push worker", http.StatusUnauthorized)
			return
		}
		var push pushJSON
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxManifest))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&push); err != nil {
			http.Error(w, "the body is not a push's manifest: "+err.Error(), http.StatusBadRequest)
			return
		}
		answer, err := recorder.Record(r.Context(), fromWire(push))
		if err != nil {
			slog.ErrorContext(r.Context(), "Recording a push failed", "push", push.ID, "repository", push.RepoID, "error", err)
			http.Error(w, "the push could not be recorded", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// The client went away if this fails: it asks again with the push's ID.
		_ = json.NewEncoder(w).Encode(answerJSON(answer))
	})
	return mux
}

// bears reports whether r's bearer token is token, in time that doesn't depend on how much of it matches.
func bears(r *http.Request, token string) bool {
	scheme, got, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	return ok && strings.EqualFold(scheme, "Bearer") && token != "" && subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(token)) == 1
}
