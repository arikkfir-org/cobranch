package rest

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"cobranch.dev/internal/services/auth"
)

// sshKey is k as its user sees it.
func sshKey(k auth.Credential) SSHKey {
	return SSHKey{Id: k.ID, Name: k.Name, PublicKey: k.PublicKey, Fingerprint: k.Hint, CreatedAt: k.CreatedAt, LastUsedAt: timeOrNil(k.LastUsedAt)}
}

// ListMySSHKeys lists the viewer's SSH keys, newest first.
func (a api) ListMySSHKeys(ctx context.Context, _ ListMySSHKeysRequestObject) (ListMySSHKeysResponseObject, error) {
	v, ok := auth.Viewer(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	keys, err := a.sessions.SSHKeys(ctx, v.User.ID)
	if err != nil {
		return nil, fmt.Errorf("listing the SSH keys: %w", err)
	}
	return ListMySSHKeys200JSONResponse{SshKeys: nonNil(mapAll(keys, sshKey))}, nil
}

// AddMySSHKey adds an SSH key for the viewer, from their session.
func (a api) AddMySSHKey(ctx context.Context, req AddMySSHKeyRequestObject) (AddMySSHKeyResponseObject, error) {
	v, ok := auth.Viewer(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	in := auth.SSHKeyInput{PublicKey: req.Body.PublicKey}
	if req.Body.Name != nil {
		in.Name = *req.Body.Name
	}
	key, err := a.sessions.AddSSHKey(ctx, v, in)
	if errors.Is(err, auth.ErrForbidden) {
		return nil, refuse(http.StatusForbidden, "ssh_key.session_required", "Only a signed-in session adds an SSH key, never a token or an API key.")
	}
	if err != nil {
		return nil, fail(err, "ssh_key")
	}
	return AddMySSHKey201JSONResponse(sshKey(key)), nil
}

// DeleteMySSHKey deletes one of the viewer's SSH keys.
func (a api) DeleteMySSHKey(ctx context.Context, req DeleteMySSHKeyRequestObject) (DeleteMySSHKeyResponseObject, error) {
	v, ok := auth.Viewer(ctx)
	if !ok {
		return nil, errUnauthenticated
	}
	err := a.sessions.DeleteSSHKey(ctx, v.User.ID, req.Id)
	if errors.Is(err, auth.ErrNotFound) {
		return nil, refuse(http.StatusNotFound, "ssh_key.not_found", "You have no such SSH key.")
	}
	if err != nil {
		return nil, fmt.Errorf("deleting the SSH key: %w", err)
	}
	return DeleteMySSHKey204Response{}, nil
}
