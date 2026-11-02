package code_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"cobranch.dev/internal/services/code"
)

var previewsCluster = uuid.MustParse("6f1c2b8e-3d4a-4e5f-9a1b-2c3d4e5f6a7b")
var parseDeliveryTests = []struct {
	name     string
	settings string
	want     code.Delivery
	wantErr  string
}{
	{name: "an empty file", settings: ""},
	{name: "no delivery", settings: "branches:\n  main:\n"},
	{name: "a delivery of nothing", settings: "delivery:\n"},
	{name: "the design's example", settings: `delivery:
  targets:
    production:
      branches: [main]
      namespace: shop
    previews:
      branches: ["feature/**", "fix-*"]
      cluster: 6f1c2b8e-3d4a-4e5f-9a1b-2c3d4e5f6a7b
      namespace: shop-${branch.slug}
  roots:
    - path: deploy/chart
      helm:
        release: shop
        valuesFiles: ["values-${target}.yaml"]
        values:
          image:
            tag: ${commit.short}
          replicas: 2
    - path: deploy/overlays/${target}
      kustomize: {}
    - path: deploy/extras
      targets: [previews]
      substitute: true
`, want: code.Delivery{
		Targets: []code.Target{
			{Name: "production", Branches: []string{"main"}, Namespace: "shop"},
			{Name: "previews", Branches: []string{"feature/**", "fix-*"}, Cluster: &previewsCluster, Namespace: "shop-${branch.slug}"},
		},
		Roots: []code.Root{
			{Path: "deploy/chart", Helm: &code.HelmRoot{Release: "shop", ValuesFiles: []string{"values-${target}.yaml"},
				Values: map[string]any{"image": map[string]any{"tag": "${commit.short}"}, "replicas": 2}}},
			{Path: "deploy/overlays/${target}", Kustomize: &code.KustomizeRoot{}},
			{Path: "deploy/extras", Targets: []string{"previews"}, Substitute: true},
		},
	}},
	{name: "a chart with nothing set", settings: "delivery:\n  roots:\n    - path: chart\n      helm:\n",
		want: code.Delivery{Roots: []code.Root{{Path: "chart", Helm: &code.HelmRoot{}}}}},
	{name: "a kustomization of null", settings: "delivery:\n  roots:\n    - path: k\n      kustomize:\n",
		want: code.Delivery{Roots: []code.Root{{Path: "k", Kustomize: &code.KustomizeRoot{}}}}},
	{name: "an escaped variable", settings: "delivery:\n  roots:\n    - path: $${dir}\n",
		want: code.Delivery{Roots: []code.Root{{Path: "$${dir}"}}}},
	{name: "keys for others left alone", settings: "jobs: [whatever]\ndelivery:\n  targets:\n    prod:\n      branches: [main]\n      namespace: prod\n",
		want: code.Delivery{Targets: []code.Target{{Name: "prod", Branches: []string{"main"}, Namespace: "prod"}}}},
	{name: "not a mapping", settings: "- delivery\n", wantErr: "/cobranch.yaml: line 1: the settings must be a mapping"},
	{name: "delivery twice", settings: "delivery:\ndelivery:\n", wantErr: `/cobranch.yaml: line 2: the settings names "delivery" twice`},
	{name: "delivery as a list", settings: "delivery: [prod]\n", wantErr: "/cobranch.yaml: line 1: delivery must be a mapping"},
	{name: "an unknown key", settings: "delivery:\n  clusters: {}\n", wantErr: `/cobranch.yaml: line 2: delivery takes no "clusters"`},
	{name: "targets as a list", settings: "delivery:\n  targets: [prod]\n", wantErr: "/cobranch.yaml: line 2: targets must map targets' names to their settings"},
	{name: "a target's bad name", settings: "delivery:\n  targets:\n    Prod:\n      branches: [main]\n      namespace: p\n",
		wantErr: "/cobranch.yaml: line 3: a target's name: 1 to 40 lowercase letters, digits and '-', starting with a letter"},
	{name: "a target twice", settings: "delivery:\n  targets:\n    prod:\n    prod:\n", wantErr: `/cobranch.yaml: line 4: targets names "prod" twice`},
	{name: "a target of nothing", settings: "delivery:\n  targets:\n    prod:\n", wantErr: "/cobranch.yaml: line 3: prod needs branches and a namespace"},
	{name: "a target's unknown key", settings: "delivery:\n  targets:\n    prod:\n      branch: main\n", wantErr: `/cobranch.yaml: line 4: prod takes no "branch"`},
	{name: "a target without branches", settings: "delivery:\n  targets:\n    prod:\n      namespace: p\n", wantErr: "/cobranch.yaml: line 4: prod needs branches"},
	{name: "branches as a string", settings: "delivery:\n  targets:\n    prod:\n      branches: main\n      namespace: p\n",
		wantErr: "/cobranch.yaml: line 4: branches must be a list of branch patterns"},
	{name: "no branches", settings: "delivery:\n  targets:\n    prod:\n      branches: []\n      namespace: p\n",
		wantErr: "/cobranch.yaml: line 4: branches must name at least one branch pattern"},
	{name: "a cluster that isn't an ID", settings: "delivery:\n  targets:\n    prod:\n      branches: [main]\n      cluster: production\n      namespace: p\n",
		wantErr: "/cobranch.yaml: line 5: cluster must be the ID of the secret that holds its kubeconfig"},
	{name: "a target without a namespace", settings: "delivery:\n  targets:\n    prod:\n      branches: [main]\n", wantErr: "/cobranch.yaml: line 4: prod needs a namespace"},
	{name: "a namespace that is a number", settings: "delivery:\n  targets:\n    prod:\n      branches: [main]\n      namespace: 42\n",
		wantErr: "/cobranch.yaml: line 5: namespace must be a string"},
	{name: "a namespace naming itself", settings: "delivery:\n  targets:\n    prod:\n      branches: [main]\n      namespace: x-${namespace}\n",
		wantErr: "/cobranch.yaml: line 5: ${namespace} is not a variable: use ${target}, ${branch}, ${branch.slug}, ${commit}, ${commit.short}, ${namespace}, ${repository}, ${organization}"},
	{name: "an unknown variable", settings: "delivery:\n  targets:\n    prod:\n      branches: [main]\n      namespace: x-${env}\n",
		wantErr: "/cobranch.yaml: line 5: ${env} is not a variable: use ${target}, ${branch}, ${branch.slug}, ${commit}, ${commit.short}, ${namespace}, ${repository}, ${organization}"},
	{name: "roots as a mapping", settings: "delivery:\n  roots:\n    chart: {}\n", wantErr: "/cobranch.yaml: line 3: roots must be a list of directories to render"},
	{name: "a root as a path", settings: "delivery:\n  roots: [chart]\n", wantErr: "/cobranch.yaml: line 2: a root must be a mapping"},
	{name: "a root's unknown key", settings: "delivery:\n  roots:\n    - path: c\n      chart: {}\n", wantErr: `/cobranch.yaml: line 4: a root takes no "chart"`},
	{name: "a root without a path", settings: "delivery:\n  roots:\n    - helm: {}\n", wantErr: "/cobranch.yaml: line 3: a root needs a path"},
	{name: "a root of both", settings: "delivery:\n  roots:\n    - path: c\n      helm: {}\n      kustomize: {}\n",
		wantErr: "/cobranch.yaml: line 5: a root renders with helm or kustomize, not both"},
	{name: "a kustomization with settings", settings: "delivery:\n  roots:\n    - path: k\n      kustomize:\n        prune: true\n",
		wantErr: `/cobranch.yaml: line 5: kustomize takes no "prune"`},
	{name: "a chart's unknown key", settings: "delivery:\n  roots:\n    - path: c\n      helm:\n        version: 1.0.0\n",
		wantErr: `/cobranch.yaml: line 5: helm takes no "version"`},
	{name: "a release that is a number", settings: "delivery:\n  roots:\n    - path: c\n      helm:\n        release: 7\n",
		wantErr: "/cobranch.yaml: line 5: release must be a string"},
	{name: "values files as a string", settings: "delivery:\n  roots:\n    - path: c\n      helm:\n        valuesFiles: values.yaml\n",
		wantErr: "/cobranch.yaml: line 5: valuesFiles must be a list of files"},
	{name: "a values file's unknown variable", settings: "delivery:\n  roots:\n    - path: c\n      helm:\n        valuesFiles:\n          - values-${env}.yaml\n",
		wantErr: "/cobranch.yaml: line 6: ${env} is not a variable: use ${target}, ${branch}, ${branch.slug}, ${commit}, ${commit.short}, ${namespace}, ${repository}, ${organization}"},
	{name: "values as a list", settings: "delivery:\n  roots:\n    - path: c\n      helm:\n        values: [a]\n",
		wantErr: "/cobranch.yaml: line 5: values must be a mapping"},
	{name: "a value's unknown variable", settings: "delivery:\n  roots:\n    - path: c\n      helm:\n        values:\n          image:\n            tag: ${sha}\n",
		wantErr: "/cobranch.yaml: line 7: ${sha} is not a variable: use ${target}, ${branch}, ${branch.slug}, ${commit}, ${commit.short}, ${namespace}, ${repository}, ${organization}"},
	{name: "a value's key naming a variable", settings: "delivery:\n  roots:\n    - path: c\n      helm:\n        values:\n          hosts:\n            ${target}: on\n",
		wantErr: `/cobranch.yaml: line 7: values' keys take no variables: "${target}"`},
	{name: "a value's key writing a variable", settings: "delivery:\n  roots:\n    - path: c\n      helm:\n        values:\n          $${target}: on\n",
		wantErr: `/cobranch.yaml: line 6: values' keys take no variables: "$${target}"`},
	{name: "a root's targets as a name", settings: "delivery:\n  roots:\n    - path: c\n      targets: prod\n",
		wantErr: "/cobranch.yaml: line 4: targets must be a list of targets' names"},
	{name: "a root's target twice", settings: "delivery:\n  roots:\n    - path: c\n      targets:\n        - prod\n        - prod\n",
		wantErr: "/cobranch.yaml: line 6: target prod is named twice"},
	{name: "a root's bad target", settings: "delivery:\n  roots:\n    - path: c\n      targets: [Prod]\n",
		wantErr: "/cobranch.yaml: line 4: targets must be a list of targets' names"},
	{name: "substitute as a string", settings: "delivery:\n  roots:\n    - path: c\n      substitute: \"yes\"\n",
		wantErr: "/cobranch.yaml: line 4: substitute must be true or false"},
	{name: "not YAML", settings: "delivery: [\n", wantErr: "/cobranch.yaml: line 1: did not find expected node content"},
}

func TestParseDelivery(t *testing.T) {
	for _, tt := range parseDeliveryTests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := code.ParseDelivery([]byte(tt.settings))
			if tt.wantErr != "" {
				var bad *code.SettingsError
				if !errors.As(err, &bad) || err.Error() != tt.wantErr {
					t.Fatalf("ParseDelivery() = %v, want *SettingsError %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseDelivery() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestExpand(t *testing.T) {
	values := map[string]string{"branch": "feature/login", "commit.short": "3b7d0c4"}
	tests := []struct {
		name, s, want string
		wantErr       bool
	}{
		{name: "nothing to expand", s: "shop", want: "shop"},
		{name: "one variable", s: "api-${branch}", want: "api-feature/login"},
		{name: "two variables", s: "${branch}@${commit.short}", want: "feature/login@3b7d0c4"},
		{name: "an escape", s: "$${branch} is ${branch}", want: "${branch} is feature/login"},
		{name: "a lone dollar", s: "cost: $5", want: "cost: $5"},
		{name: "a variable without a value", s: "${namespace}", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := code.Expand(tt.s, values)
			if tt.wantErr {
				if !errors.Is(err, code.ErrUnknownVariable) {
					t.Fatalf("Expand() = %q, %v, want ErrUnknownVariable", got, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("Expand() = %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}
