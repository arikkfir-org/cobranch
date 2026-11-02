package config

import (
	"maps"
	"strings"
	"testing"
	"time"
)

// controllerEnv is what the chart renders for a controller, with changes; an empty value unsets its variable.
func controllerEnv(changes map[string]string) map[string]string {
	env := map[string]string{"COBRANCH_JOBS_SCRIPT_IMAGE": "cobranch:v1", "COBRANCH_JOBS_CHECKOUT_IMAGE": "cobranch:v1",
		"COBRANCH_JOBS_NAMESPACE_PREFIX": "cobranch-jobs-", "COBRANCH_JOBS_INSTALLATION": "cobranch-cobranch", "COBRANCH_JOBS_HOME": "cobranch",
		"COBRANCH_JOBS_SERVICE_ACCOUNT": "cobranch-controller", "COBRANCH_JOBS_RELEASE": "cobranch", "COBRANCH_JOBS_RUNNER_ROLE": "cobranch-cobranch-jobs-runner"}
	maps.Copy(env, changes)
	maps.DeleteFunc(env, func(_, v string) bool { return v == "" })
	return env
}

func TestLoadController(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		check   func(Controller) bool
		wantErr string
	}{
		{name: "defaults", env: controllerEnv(nil), check: func(c Controller) bool {
			return c.PollInterval == 5*time.Second && c.SweepInterval == 5*time.Minute && c.Pods && c.Lease == 2*time.Minute &&
				c.Timeout == time.Hour && c.MaxTimeout == 24*time.Hour && c.LogsMaxBytes == 10<<20 && c.PodSecurity == "restricted" &&
				c.Quota.Value == nil && c.Affinity.Value == nil
		}},
		{name: "the chart's maps and lists", env: controllerEnv(map[string]string{
			"COBRANCH_JOBS_NAMESPACE_LABELS": `{"example.com/team":"ci"}`, "COBRANCH_JOBS_EGRESS_EXCEPT": "10.0.0.0/8,fc00::/7",
			"COBRANCH_JOBS_EGRESS_DNS": `[{"podSelector":{"matchLabels":{"k8s-app":"kube-dns"}},"namespaceSelector":{}}]`,
			"COBRANCH_JOBS_RESOURCES":  `{"requests":{"cpu":"100m"},"limits":{"memory":"1Gi"}}`, "COBRANCH_JOBS_QUOTA": `{"pods":"10"}`,
			"COBRANCH_JOBS_TOLERATIONS": `[{"key":"pool","operator":"Equal","value":"ci","effect":"NoSchedule"}]`,
			"COBRANCH_JOBS_AFFINITY":    `{"nodeAffinity":{}}`}),
			check: func(c Controller) bool {
				return c.NamespaceLabels.Value["example.com/team"] == "ci" && len(c.EgressExcept) == 2 && len(c.EgressDNS.Value) == 1 &&
					c.Resources.Value.Limits.Memory().String() == "1Gi" && c.Quota.Value.Pods().String() == "10" &&
					len(c.Tolerations.Value) == 1 && c.Affinity.Value.NodeAffinity != nil
			}},
		{name: "no checkout image", env: controllerEnv(map[string]string{"COBRANCH_JOBS_CHECKOUT_IMAGE": ""}), wantErr: "JOBS_CHECKOUT_IMAGE"},
		{name: "resources that aren't JSON", env: controllerEnv(map[string]string{"COBRANCH_JOBS_RESOURCES": "cpu=1"}), wantErr: "JOBS_RESOURCES"},
		{name: "a default beyond the most", env: controllerEnv(map[string]string{"COBRANCH_JOBS_TIMEOUT": "25h"}), wantErr: "within the most"},
		{name: "no poll", env: controllerEnv(map[string]string{"COBRANCH_JOBS_POLL_INTERVAL": "0s"}), wantErr: "must be positive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clean(t, tt.env)
			got, err := LoadController()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("LoadController() = %v, want an error naming %s", err, tt.wantErr)
				}
				return
			}
			if err != nil || !tt.check(got) {
				t.Errorf("LoadController() = %+v, %v", got, err)
			}
		})
	}
}

func TestLoadCheckout(t *testing.T) {
	clean(t, map[string]string{"COBRANCH_GIT_URL": "http://git/acme/api.git", "COBRANCH_TOKEN": "cbr_t", "COBRANCH_SHA": "abc"})
	if got, err := LoadCheckout(); err != nil || got != (Checkout{GitURL: "http://git/acme/api.git", Token: "cbr_t", SHA: "abc", Workspace: "/workspace"}) {
		t.Errorf("LoadCheckout() = %+v, %v", got, err)
	}
	clean(t, map[string]string{"COBRANCH_GIT_URL": "http://git/acme/api.git", "COBRANCH_SHA": "abc"})
	if _, err := LoadCheckout(); err == nil {
		t.Error("LoadCheckout() without a token succeeded")
	}
}
