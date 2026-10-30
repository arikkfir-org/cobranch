package auth

import "context"

type viewerKey struct{}

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
