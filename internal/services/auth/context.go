package auth

import "context"

type (
	viewerKey struct{}
	tokenKey  struct{}
)

// WithViewer returns ctx with who the request's credential resolved to; the servers' middleware sets it.
func WithViewer(ctx context.Context, a Authenticated) context.Context {
	return context.WithValue(ctx, viewerKey{}, a)
}

// Viewer returns who the request ctx belongs to resolved to, and whether it resolved to anyone: without a credential,
// the request is Anonymous's.
func Viewer(ctx context.Context) (Authenticated, bool) {
	a, ok := ctx.Value(viewerKey{}).(Authenticated)
	return a, ok
}

// WithToken returns ctx with the token the request's credential carried, a session's, a personal token or an API
// key's secret; the servers' middleware sets it beside the viewer, so that a call another workload answers for the
// viewer, such as the Git server's internal call, sends it on and that workload decides for itself what the viewer
// may read.
func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenKey{}, token)
}

// Token returns the token the request ctx belongs to carried, and whether it carried one: without a credential, the
// request is Anonymous's.
func Token(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(tokenKey{}).(string)
	return token, ok && token != ""
}
