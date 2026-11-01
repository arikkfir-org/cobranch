package graphql

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/auth"
)

// sshKey is k as its user sees it.
func sshKey(k auth.Credential) *SSHKey {
	return &SSHKey{ID: k.ID.String(), Name: k.Name, PublicKey: k.PublicKey, Fingerprint: k.Hint, CreatedAt: k.CreatedAt, LastUsedAt: timeOrNil(k.LastUsedAt)}
}

// sshKeys lists the viewer's SSH keys.
func (r *Resolver) sshKeys(ctx context.Context) ([]*SSHKey, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := r.Sessions.SSHKeys(ctx, v.User.ID)
	if err != nil {
		return nil, fail(ctx, err, "ssh_key", "Listing the viewer's SSH keys")
	}
	return nonNil(mapAll(keys, sshKey)), nil
}

// addSSHKey adds an SSH key for the viewer, from their session.
func (r *Resolver) addSSHKey(ctx context.Context, input SSHKeyInput) (*SSHKey, error) {
	v, err := viewer(ctx)
	if err != nil {
		return nil, err
	}
	in := auth.SSHKeyInput{PublicKey: input.PublicKey}
	if input.Name != nil {
		in.Name = *input.Name
	}
	key, err := r.Sessions.AddSSHKey(ctx, v, in)
	if errors.Is(err, auth.ErrForbidden) {
		return nil, refuse(ctx, forbidden, "ssh_key.session_required", "only a signed-in session adds an SSH key, never a token or an API key")
	}
	if err != nil {
		return nil, fail(ctx, err, "ssh_key", "Adding the SSH key")
	}
	return sshKey(key), nil
}

// deleteSSHKey deletes one of the viewer's SSH keys: false when the viewer has no such key.
func (r *Resolver) deleteSSHKey(ctx context.Context, id string) (bool, error) {
	v, err := viewer(ctx)
	if err != nil {
		return false, err
	}
	keyID, err := uuid.Parse(id)
	if err != nil {
		return false, nil
	}
	err = r.Sessions.DeleteSSHKey(ctx, v.User.ID, keyID)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		return false, nil
	case err != nil:
		slog.ErrorContext(ctx, "Deleting an SSH key failed", "error", err)
		return false, errors.New("the SSH key could not be deleted")
	}
	return true, nil
}
