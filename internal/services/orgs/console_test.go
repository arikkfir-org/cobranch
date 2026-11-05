package orgs_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"cobranch.dev/internal/services/orgs"
)

// TestReservedSlugsHoldTheConsolePages checks that every page the Console draws at its own path, a module of
// pages/ with a default export, has its path among the slugs no organization may take: an organization with that
// slug would be hidden behind the page.
func TestReservedSlugsHoldTheConsolePages(t *testing.T) {
	files, err := filepath.Glob("../../../web/console/public/assets/pages/*.js")
	if err != nil || len(files) == 0 {
		t.Fatalf("the Console's own pages: %v, %v", files, err)
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), "\nexport default ") {
			continue
		}
		if page := strings.TrimSuffix(filepath.Base(f), ".js"); !slices.Contains(orgs.ReservedSlugs, page) {
			t.Errorf("ReservedSlugs lacks %q, the Console's page %s", page, f)
		}
	}
}
