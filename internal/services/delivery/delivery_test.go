package delivery_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"cobranch.dev/internal/services/code"
	"cobranch.dev/internal/services/delivery"
)

func TestMatch(t *testing.T) {
	tests := []struct {
		pattern, branch string
		want            bool
	}{
		{"main", "main", true},
		{"main", "mainline", false},
		{"release-*", "release-1.2", true},
		{"release-*", "release-", true},
		{"feature/*", "feature/login", true},
		{"feature/*", "feature/login/form", false},
		{"feature/**", "feature/login/form", true},
		{"feature/**", "feature/", true},
		{"**", "any/thing", true},
		{"v?", "v1", true},
		{"v?", "v10", false},
		{"?", "/", false},
		{"fix.1", "fix-1", false},
		{"fix.1", "fix.1", true},
		{"[a]", "[a]", true},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+" "+tt.branch, func(t *testing.T) {
			if got := delivery.Match(tt.pattern, tt.branch); got != tt.want {
				t.Errorf("Match(%q, %q) = %v, want %v", tt.pattern, tt.branch, got, tt.want)
			}
		})
	}
}

func TestTargetsOf(t *testing.T) {
	d := code.Delivery{Targets: []code.Target{
		{Name: "production", Branches: []string{"main"}},
		{Name: "previews", Branches: []string{"feature/**", "main"}},
		{Name: "staging", Branches: []string{"release-*"}},
	}}
	tests := []struct {
		branch string
		want   []string
	}{
		{"main", []string{"production", "previews"}},
		{"feature/a/b", []string{"previews"}},
		{"release-2", []string{"staging"}},
		{"chore", nil},
	}
	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			var got []string
			for _, target := range delivery.TargetsOf(d, tt.branch) {
				got = append(got, target.Name)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("TargetsOf(%q) = %v, want %v", tt.branch, got, tt.want)
			}
		})
	}
}

func TestTarget(t *testing.T) {
	d := code.Delivery{Targets: []code.Target{{Name: "production"}, {Name: "previews"}}}
	if got, err := delivery.Target(d, "previews"); err != nil || got.Name != "previews" {
		t.Errorf("Target(previews) = %+v, %v", got, err)
	}
	if _, err := delivery.Target(d, "staging"); !errors.Is(err, delivery.ErrInvalid) || err.Error() != "invalid delivery: no target staging" {
		t.Errorf("Target(staging) = %v, want ErrInvalid", err)
	}
}

func TestSlug(t *testing.T) {
	tests := []struct{ branch, want string }{
		{"main", "main"},
		{"feature/Login", "feature-login"},
		{"fix--this__now", "fix-this-now"},
		{"/leading/and/trailing/", "leading-and-trailing"},
		{"日本語", "branch"},
		{"", "branch"},
		{strings.Repeat("a", 45), strings.Repeat("a", 40)},
		{strings.Repeat("a", 39) + "/b", strings.Repeat("a", 39)},
	}
	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			if got := delivery.Slug(tt.branch); got != tt.want {
				t.Errorf("Slug(%q) = %q, want %q", tt.branch, got, tt.want)
			}
		})
	}
}

// planContext is the delivery the plans are for, and planVars its variables for target previews.
var planContext = delivery.Context{Branch: "feature/Login", Commit: "3b7d0c4e9f0a1b2c3d4e5f60718293a4b5c6d7e8", Repository: "shop", Organization: "acme"}
var planVars = map[string]string{"target": "previews", "branch": "feature/Login", "branch.slug": "feature-login",
	"commit": planContext.Commit, "commit.short": "3b7d0c4", "repository": "shop", "organization": "acme", "namespace": "shop-feature-login"}
var previews = code.Target{Name: "previews", Namespace: "${repository}-${branch.slug}"}
var planForTests = []struct {
	name        string
	roots       []code.Root
	target      code.Target
	planContext delivery.Context
	want        delivery.Plan
	wantErr     string
}{
	{name: "every kind", target: previews, planContext: planContext, roots: []code.Root{
		{Path: "deploy/chart/", Helm: &code.HelmRoot{ValuesFiles: []string{"values-${target}.yaml", "../shared.yaml"},
			Values: map[string]any{"image": map[string]any{"tag": "${commit.short}"}, "hosts": []any{"${branch.slug}.example.com"}, "replicas": 2}}},
		{Path: "deploy/overlays/${target}", Kustomize: &code.KustomizeRoot{}},
		{Path: "./deploy/extras", Substitute: true},
	}, want: delivery.Plan{Namespace: "shop-feature-login", Variables: planVars, Roots: []delivery.PlannedRoot{
		{Path: "deploy/chart", Kind: delivery.Helm, Release: "chart", ValuesFiles: []string{"deploy/chart/values-previews.yaml", "deploy/shared.yaml"},
			Values: map[string]any{"image": map[string]any{"tag": "3b7d0c4"}, "hosts": []any{"feature-login.example.com"}, "replicas": 2}},
		{Path: "deploy/overlays/previews", Kind: delivery.Kustomize},
		{Path: "deploy/extras", Kind: delivery.Manifests, Substitute: true},
	}}},
	{name: "roots for other targets left out", target: code.Target{Name: "production", Namespace: "shop"}, planContext: planContext, roots: []code.Root{
		{Path: "a", Targets: []string{"previews"}},
		{Path: "b", Targets: []string{"previews", "production"}},
		{Path: "c"},
	}, want: delivery.Plan{Namespace: "shop", Variables: map[string]string{"target": "production", "branch": "feature/Login", "branch.slug": "feature-login",
		"commit": planContext.Commit, "commit.short": "3b7d0c4", "repository": "shop", "organization": "acme", "namespace": "shop"},
		Roots: []delivery.PlannedRoot{{Path: "b", Kind: delivery.Manifests}, {Path: "c", Kind: delivery.Manifests}}}},
	{name: "a named release", target: previews, planContext: planContext, roots: []code.Root{{Path: "chart", Helm: &code.HelmRoot{Release: "shop-${branch.slug}"}}},
		want: delivery.Plan{Namespace: "shop-feature-login", Variables: planVars, Roots: []delivery.PlannedRoot{{Path: "chart", Kind: delivery.Helm, Release: "shop-feature-login"}}}},
	{name: "a short commit", target: code.Target{Name: "t", Namespace: "n-${commit.short}"}, planContext: delivery.Context{Branch: "main", Commit: "abc"},
		want: delivery.Plan{Namespace: "n-abc", Variables: map[string]string{"target": "t", "branch": "main", "branch.slug": "main", "commit": "abc", "commit.short": "abc", "namespace": "n-abc"}}},
	{name: "a namespace naming what has no value", target: code.Target{Name: "t", Namespace: "${organization}-x"}, planContext: delivery.Context{Branch: "main"},
		wantErr: "invalid delivery: target t's namespace: unknown variable ${organization}"},
	{name: "a namespace that isn't a DNS label", target: code.Target{Name: "t", Namespace: "${branch}"}, planContext: planContext,
		wantErr: `invalid delivery: target t's namespace "feature/Login" is not a DNS label: 1 to 63 lowercase letters, digits and '-'`},
	{name: "a namespace too long", target: code.Target{Name: "t", Namespace: strings.Repeat("n", 30) + "-${branch.slug}-" + strings.Repeat("n", 30)}, planContext: planContext,
		wantErr: "is not a DNS label"},
	{name: "a path leaving the repository", target: previews, planContext: planContext, roots: []code.Root{{Path: "deploy/../../etc"}},
		wantErr: `invalid delivery: root 1: path "deploy/../../etc" must name a directory inside the repository`},
	{name: "an absolute path", target: previews, planContext: planContext, roots: []code.Root{{Path: "c"}, {Path: "/etc"}},
		wantErr: `invalid delivery: root 2: path "/etc" must name a directory inside the repository`},
	{name: "the repository's root", target: previews, planContext: planContext, roots: []code.Root{{Path: "deploy/.."}},
		wantErr: `invalid delivery: root 1: path "deploy/.." must name a directory inside the repository`},
	{name: "a values file leaving the repository", target: previews, planContext: planContext, roots: []code.Root{{Path: "chart", Helm: &code.HelmRoot{ValuesFiles: []string{"../../v.yaml"}}}},
		wantErr: `invalid delivery: root 1: values file ../../v.yaml: path "../v.yaml" must name a directory inside the repository`},
	{name: "a release Helm refuses", target: previews, planContext: planContext, roots: []code.Root{{Path: "Chart", Helm: &code.HelmRoot{}}},
		wantErr: `invalid delivery: root 1: release "Chart": at most 53 lowercase letters, digits, '-' and '.'`},
	{name: "a release too long", target: previews, planContext: planContext, roots: []code.Root{{Path: "c", Helm: &code.HelmRoot{Release: strings.Repeat("r", 54)}}},
		wantErr: "at most 53 lowercase letters"},
	{name: "a value naming what has no value", target: previews, planContext: delivery.Context{Branch: "main"}, roots: []code.Root{{Path: "c", Helm: &code.HelmRoot{Values: map[string]any{"a": []any{"${commit}"}}}}},
		wantErr: "invalid delivery: target previews's namespace: unknown variable ${repository}"},
	{name: "a value naming the commit it lacks", target: code.Target{Name: "t", Namespace: "n"}, planContext: delivery.Context{Branch: "main"},
		roots:   []code.Root{{Path: "c", Helm: &code.HelmRoot{Values: map[string]any{"a": []any{"${commit}"}}}}},
		wantErr: "invalid delivery: root 1: values.a[0]: unknown variable ${commit}"},
}

func TestPlanFor(t *testing.T) {
	for _, tt := range planForTests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := delivery.PlanFor(tt.roots, tt.target, tt.planContext)
			if tt.wantErr != "" {
				if !errors.Is(err, delivery.ErrInvalid) || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("PlanFor() = %v, want ErrInvalid %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("PlanFor() =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}
