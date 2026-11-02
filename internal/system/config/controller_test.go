package config

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
)

// controllerEnv is what the chart renders for a controller, with changes; an empty value unsets its variable.
func controllerEnv(changes map[string]string) map[string]string {
	env := map[string]string{"COBRANCH_JOBS_SCRIPT_IMAGE": "cobranch:v1", "COBRANCH_JOBS_CHECKOUT_IMAGE": "cobranch:v1",
		"COBRANCH_JOBS_NAMESPACE_PREFIX": "cobranch-jobs-", "COBRANCH_JOBS_INSTALLATION": "cobranch-cobranch", "COBRANCH_JOBS_HOME": "cobranch",
		"COBRANCH_JOBS_SERVICE_ACCOUNT": "cobranch-controller", "COBRANCH_JOBS_RELEASE": "cobranch", "COBRANCH_JOBS_RUNNER_ROLE": "cobranch-cobranch-jobs-runner",
		"COBRANCH_JOBS_OWNER_ROLE": "cobranch-cobranch-controller"}
	maps.Copy(env, changes)
	maps.DeleteFunc(env, func(_, v string) bool { return v == "" })
	return env
}

// controllerCase is a test of LoadController: the environment, and what it reads from it or the error it names.
type controllerCase struct {
	name    string
	env     map[string]string
	check   func(Controller) bool
	wantErr string
}

func TestLoadController(t *testing.T) {
	checkControllers(t, []controllerCase{
		{name: "defaults", env: controllerEnv(nil), check: func(c Controller) bool {
			return c.PollInterval == 5*time.Second && c.SweepInterval == 5*time.Minute && c.Pods && c.Lease == 2*time.Minute &&
				c.Timeout == time.Hour && c.MaxTimeout == 24*time.Hour && c.LogsMaxBytes == 10<<20 && c.PodSecurity == "restricted" &&
				c.Quota.Value == nil && c.Affinity.Value == nil && c.OwnerRole == "cobranch-cobranch-controller" && c.Agents.Image == "cobranch:v1" &&
				c.Agents.Timeout == 24*time.Hour && c.Agents.MaxTurns == 200 && c.Agents.ShellTimeout == 10*time.Minute && c.Agents.MaxTokens == 8192 &&
				c.Agents.Resources.Value.Limits == nil && !c.InCluster &&
				c.RenderTimeout == 10*time.Minute && c.HealthInterval == 15*time.Second && c.DeliveryPodSecurity == "restricted" &&
				c.RendersKeep == 10
		}},
		{name: "the chart's maps and lists", env: controllerEnv(map[string]string{
			"COBRANCH_JOBS_NAMESPACE_LABELS": `{"example.com/team":"ci"}`, "COBRANCH_JOBS_EGRESS_EXCEPT": "10.0.0.0/8,fc00::/7",
			"COBRANCH_JOBS_EGRESS_DNS": `[{"podSelector":{"matchLabels":{"k8s-app":"kube-dns"}},"namespaceSelector":{}}]`,
			"COBRANCH_JOBS_RESOURCES":  `{"requests":{"cpu":"100m"},"limits":{"memory":"1Gi"}}`, "COBRANCH_JOBS_QUOTA": `{"pods":"10"}`,
			"COBRANCH_JOBS_TOLERATIONS": `[{"key":"pool","operator":"Equal","value":"ci","effect":"NoSchedule"}]`,
			"COBRANCH_JOBS_AFFINITY":    `{"nodeAffinity":{}}`, "COBRANCH_JOBS_IMAGE_PULL_SECRETS": "registry,mirror"}),
			check: func(c Controller) bool {
				return c.NamespaceLabels.Value["example.com/team"] == "ci" && len(c.EgressExcept) == 2 && len(c.EgressDNS.Value) == 1 &&
					c.Resources.Value.Limits.Memory().String() == "1Gi" && c.Quota.Value.Pods().String() == "10" &&
					len(c.Tolerations.Value) == 1 && c.Affinity.Value.NodeAffinity != nil &&
					slices.Equal(c.ImagePullSecrets, []string{"registry", "mirror"})
			}},
		{name: "no checkout image", env: controllerEnv(map[string]string{"COBRANCH_JOBS_CHECKOUT_IMAGE": ""}), wantErr: "JOBS_CHECKOUT_IMAGE"},
		{name: "no owner role", env: controllerEnv(map[string]string{"COBRANCH_JOBS_OWNER_ROLE": ""}), wantErr: "JOBS_OWNER_ROLE"},
		{name: "resources that aren't JSON", env: controllerEnv(map[string]string{"COBRANCH_JOBS_RESOURCES": "cpu=1"}), wantErr: "JOBS_RESOURCES"},
		{name: "a default beyond the most", env: controllerEnv(map[string]string{"COBRANCH_JOBS_TIMEOUT": "25h"}), wantErr: "within the most"},
		{name: "no poll", env: controllerEnv(map[string]string{"COBRANCH_JOBS_POLL_INTERVAL": "0s"}), wantErr: "must be positive"},
	})
}

func TestLoadControllerAgents(t *testing.T) {
	checkControllers(t, []controllerCase{
		{name: "agents' own", env: controllerEnv(map[string]string{"COBRANCH_AGENTS_IMAGE": "tools:v2", "COBRANCH_AGENTS_TIMEOUT": "2h",
			"COBRANCH_AGENTS_MAX_TURNS": "50", "COBRANCH_AGENTS_RESOURCES": `{"limits":{"memory":"4Gi"}}`}), check: func(c Controller) bool {
			return c.Agents.Image == "tools:v2" && c.Agents.Timeout == 2*time.Hour && c.Agents.MaxTurns == 50 && c.Agents.Resources.Value.Limits.Memory().String() == "4Gi"
		}},
		{name: "no turns", env: controllerEnv(map[string]string{"COBRANCH_AGENTS_MAX_TURNS": "0"}), wantErr: "COBRANCH_AGENTS_MAX_TURNS"},
		{name: "a provider's gateway", env: controllerEnv(map[string]string{"COBRANCH_AGENTS_PROVIDER_URLS": `{"anthropic":"https://gateway.example.com/anthropic"}`}),
			check: func(c Controller) bool {
				return c.Agents.ProviderURLs.Value["anthropic"] == "https://gateway.example.com/anthropic"
			}},
		{name: "a provider's gateway that isn't a URL", env: controllerEnv(map[string]string{"COBRANCH_AGENTS_PROVIDER_URLS": `{"anthropic":"gateway:8080"}`}),
			wantErr: "COBRANCH_AGENTS_PROVIDER_URLS names anthropic"},
	})
}

func TestLoadControllerDelivery(t *testing.T) {
	checkControllers(t, []controllerCase{
		{name: "delivery into this cluster", env: controllerEnv(map[string]string{"COBRANCH_DELIVERY_IN_CLUSTER": "true",
			"COBRANCH_DELIVERY_ACCOUNT": "cobranch-delivery", "COBRANCH_DELIVERY_ROLE": "cobranch-cobranch-delivery",
			"COBRANCH_DELIVERY_FENCE_ROLE": "cobranch-cobranch-environments", "COBRANCH_DELIVERY_POD_SECURITY": "baseline",
			"COBRANCH_DELIVERY_INGRESS_FROM":   `[{"matchLabels":{"kubernetes.io/metadata.name":"gateway"}}]`,
			"COBRANCH_DELIVERY_QUOTA":          `{"pods":"20"}`,
			"COBRANCH_DELIVERY_RENDER_TIMEOUT": "5m", "COBRANCH_DELIVERY_RENDER_RESOURCES": `{"limits":{"memory":"512Mi"}}`}),
			check: func(c Controller) bool {
				return c.InCluster && c.Account == "cobranch-delivery" && c.DeliveryPodSecurity == "baseline" && len(c.IngressFrom.Value) == 1 &&
					c.DeliveryQuota.Value.Pods().String() == "20" && c.RenderTimeout == 5*time.Minute &&
					c.RenderResources.Value.Limits.Memory().String() == "512Mi" && c.PodSecurity == "restricted"
			}},
		{name: "delivery into this cluster without its account", env: controllerEnv(map[string]string{"COBRANCH_DELIVERY_IN_CLUSTER": "true"}),
			wantErr: "COBRANCH_DELIVERY_ACCOUNT"},
		{name: "no render timeout", env: controllerEnv(map[string]string{"COBRANCH_DELIVERY_RENDER_TIMEOUT": "0s"}), wantErr: "must be positive"},
		{name: "no renders kept", env: controllerEnv(map[string]string{"COBRANCH_DELIVERY_RENDERS_KEEP": "0"}), wantErr: "COBRANCH_DELIVERY_RENDERS_KEEP"},
	})
}

// checkControllers runs tests against LoadController.
func checkControllers(t *testing.T, tests []controllerCase) {
	t.Helper()
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
