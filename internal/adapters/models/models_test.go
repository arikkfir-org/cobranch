package models

import (
	"regexp"
	"slices"
	"testing"
)

func TestProviders(t *testing.T) {
	// A provider's name is a row's key in model_providers, which takes these alone.
	name := regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	got := Providers()
	if !slices.IsSorted(got) || len(slices.Compact(slices.Clone(got))) != len(got) {
		t.Errorf("Providers() = %v, want them sorted and each once", got)
	}
	for _, p := range got {
		if !name.MatchString(p) {
			t.Errorf("Providers() names %q, which model_providers refuses", p)
		}
	}
}
