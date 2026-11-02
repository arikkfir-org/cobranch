package deliverer

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/secrets"
)

// noKey is why a delivery to another cluster can't read the kubeconfig: the installation can't open secrets.
const noKey = "this installation has no secrets key to open its secrets with"

// open reaches env's cluster: this installation's own without a cluster, or the one the kubeconfig in the environment's
// cluster secret reaches, which its target reads as a run's job reads its secrets. It answers why it can't when the
// delivery should fail for it, saying so: an installation that delivers into no cluster of its own, a secret that is
// gone or that the target may not read, or a kubeconfig the controller won't use.
func (s *Service) open(ctx context.Context, env Environment) (Target, string, error) {
	if env.Cluster == nil {
		if s.target == nil {
			return nil, noCluster, nil
		}
		return s.target, "", nil
	}
	id := *env.Cluster
	if s.clusters == nil || s.secrets == nil {
		return nil, "this installation delivers to no other cluster", nil
	}
	values, err := s.secrets.Receive(ctx, env.TargetID, []uuid.UUID{id})
	var refused *secrets.RefusedError
	switch {
	case errors.As(err, &refused) && refused.Missing:
		return nil, fmt.Sprintf("secret %s, target %s's cluster, isn't there", id, env.Target), nil
	case errors.As(err, &refused):
		return nil, fmt.Sprintf("target %s may not read secret %s, its cluster: grant it Secret reader on the secret", env.Target, id), nil
	case errors.Is(err, secrets.ErrNoSecretsKey):
		return nil, noKey, nil
	case err != nil:
		return nil, "", fmt.Errorf("reading target %s's kubeconfig: %w", env.Target, err)
	}
	target, err := s.clusters.Open(ctx, values[id])
	switch {
	case errors.Is(err, ErrRefused):
		return nil, fmt.Sprintf("the kubeconfig in secret %s: %s", id, unrefused(err)), nil
	case err != nil:
		return nil, "", fmt.Errorf("opening target %s's cluster: %w", env.Target, err)
	}
	return target, "", nil
}
