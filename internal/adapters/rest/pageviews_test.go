package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestReportPageViews(t *testing.T) {
	view := func(page, navigation string, ms float64, reads int) string {
		return fmt.Sprintf(`{"page":%q,"navigation":%q,"durationMs":%v,"gitReads":%d}`, page, navigation, ms, reads)
	}
	views := func(vs ...string) string { return `{"views":[` + strings.Join(vs, ",") + `]}` }
	tests := []struct {
		name       string
		body       string
		wantStatus int
		want       []string // "page navigation count sum" of each duration recorded, then of each read count
	}{
		{name: "a first load and a move", body: views(view("repository.code", "load", 1250, 2), view("repository.file", "route", 80.5, 1)), wantStatus: 204,
			want: []string{"repository.code load 1 1.25", "repository.file route 1 0.0805", "repository.code load 1 2", "repository.file route 1 1"}},
		{name: "the sign-in and help pages", body: views(view("sign-in", "load", 300, 0), view("help", "route", 100, 0), view("help.components", "load", 500, 0)), wantStatus: 204,
			want: []string{"help route 1 0.1", "help.components load 1 0.5", "sign-in load 1 0.3", "help route 1 0", "help.components load 1 0", "sign-in load 1 0"}},
		{name: "an organization's issues", body: views(view("organization.issues", "route", 200, 0)), wantStatus: 204,
			want: []string{"organization.issues route 1 0.2", "organization.issues route 1 0"}},
		{name: "a branch's changes", body: views(view("repository.changes", "route", 400, 3)), wantStatus: 204,
			want: []string{"repository.changes route 1 0.4", "repository.changes route 1 3"}},
		{name: "the same page twice", body: views(view("organization", "route", 100, 0), view("organization", "route", 300, 0)), wantStatus: 204,
			want: []string{"organization route 2 0.4", "organization route 2 0"}},
		{name: "twenty views", body: views(slices.Repeat([]string{view("welcome", "route", 10, 0)}, 20)...), wantStatus: 204,
			want: []string{"welcome route 20 0.2", "welcome route 20 0"}},
		{name: "an organization's agents, tasks and secrets", body: views(view("organization.agents", "route", 100, 0), view("organization.tasks", "route", 200, 0), view("organization.secrets", "route", 300, 0)), wantStatus: 204,
			want: []string{"organization.agents route 1 0.1", "organization.secrets route 1 0.3", "organization.tasks route 1 0.2", "organization.agents route 1 0", "organization.secrets route 1 0", "organization.tasks route 1 0"}},
		{name: "a repository's runs, environments and secrets", body: views(view("repository.runs", "route", 100, 1), view("repository.environments", "route", 200, 0), view("repository.secrets", "load", 300, 0)), wantStatus: 204,
			want: []string{"repository.environments route 1 0.2", "repository.runs route 1 0.1", "repository.secrets load 1 0.3", "repository.environments route 1 0", "repository.runs route 1 1", "repository.secrets load 1 0"}},
		{name: "no views", body: views(), wantStatus: 400},
		{name: "too many views", body: views(slices.Repeat([]string{view("welcome", "route", 10, 0)}, 21)...), wantStatus: 400},
		{name: "a page the API lacks beside one it names", body: views(view("welcome", "route", 10, 0), view("admin", "route", 10, 0)), wantStatus: 204,
			want: []string{"welcome route 1 0.01", "welcome route 1 0"}},
		{name: "another navigation", body: views(view("welcome", "prefetch", 10, 0), view("settings", "route", 20, 0)), wantStatus: 204,
			want: []string{"settings route 1 0.02", "settings route 1 0"}},
		{name: "a negative time", body: views(view("welcome", "route", -1, 0)), wantStatus: 204},
		{name: "longer than ten minutes", body: views(view("welcome", "route", 600001, 0)), wantStatus: 204},
		{name: "too many reads", body: views(view("welcome", "route", 10, 1001)), wantStatus: 204},
		{name: "a negative read count", body: views(view("welcome", "route", 10, -1)), wantStatus: 204},
		{name: "a body larger than any report", body: views(view("welcome", "route", 10, 0) + strings.Repeat(" ", 16<<10)), wantStatus: 400},
		{name: "not JSON", body: `views`, wantStatus: 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := sdkmetric.NewManualReader()
			h := NewHandler("v1.2.3", nil, nil, nil, nil, nil, nil, nil, nil, nil, WithMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))))
			req := httptest.NewRequest("POST", "/v1/page-views", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantStatus != 204 {
				var p Problem
				if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != "request.invalid" {
					t.Errorf("body = %s, want a problem with code request.invalid", rec.Body)
				}
			}
			if got := recordedViews(t, reader); !slices.Equal(got, tt.want) {
				t.Errorf("recorded %q, want %q", got, tt.want)
			}
		})
	}
}

// TestPageViewsNameEveryConsolePage keeps the names the Console's page modules report their views under among the
// pages the API records, so a new page's views aren't skipped, and holds a module whose own page the API records to
// that name, so it isn't counted as another page's.
func TestPageViewsNameEveryConsolePage(t *testing.T) {
	for _, dir := range []struct{ glob, prefix string }{
		{glob: "../../../web/console/public/assets/pages/org/*.js", prefix: "organization."},
		{glob: "../../../web/console/public/assets/pages/repo/*.js", prefix: "repository."},
	} {
		files, err := filepath.Glob(dir.glob)
		if err != nil || len(files) == 0 {
			t.Fatalf("the Console's pages in %s: %v, %v", dir.glob, files, err)
		}
		for _, f := range files {
			own := dir.prefix + strings.TrimSuffix(filepath.Base(f), ".js")
			names := consoleViews(t, f, own)
			for _, name := range names {
				if !PageViewPage(name).Valid() {
					t.Errorf("%s reports its views as %q, which PageView.page in api/openapi.yaml doesn't name", f, name)
				}
			}
			if PageViewPage(own).Valid() && !slices.Contains(names, own) {
				t.Errorf("%s reports its views as %q, though the API records its own page, %q", f, names, own)
			}
		}
	}
}

// quotedName is a string literal in a page module's view.
var quotedName = regexp.MustCompile(`"([^"]+)"`)

// consoleViews reads the names the Console's page module at path reports its views under, as the Console's router
// names them: its view's, a name or those its function returns, or else own, the module's own page.
func consoleViews(t *testing.T, path, own string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(src)) {
		if view, ok := strings.CutPrefix(line, "export const view = "); ok {
			var names []string
			for _, m := range quotedName.FindAllStringSubmatch(view, -1) {
				names = append(names, m[1])
			}
			if len(names) == 0 {
				t.Errorf("%s: its view names no page on its line: %s", path, strings.TrimSpace(line))
			}
			return names
		}
	}
	return []string{own}
}

// recordedViews collects the page views' metrics from reader: "page navigation count sum" of each duration's data
// point, by page, then of each read count's.
func recordedViews(t *testing.T, reader sdkmetric.Reader) []string {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	var durations, reads []string
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch data := m.Data.(type) {
			case metricdata.Histogram[float64]:
				for _, dp := range data.DataPoints {
					durations = append(durations, point(dp.Attributes, dp.Count, fmt.Sprint(round(dp.Sum))))
				}
			case metricdata.Histogram[int64]:
				for _, dp := range data.DataPoints {
					reads = append(reads, point(dp.Attributes, dp.Count, fmt.Sprint(dp.Sum)))
				}
			default:
				t.Fatalf("recorded %s as %T, want the page views' histograms alone", m.Name, m.Data)
			}
		}
	}
	slices.Sort(durations)
	slices.Sort(reads)
	return append(durations, reads...)
}

// point describes a data point of a page view's metric, with attrs, of count measurements adding up to sum.
func point(attrs attribute.Set, count uint64, sum string) string {
	page, _ := attrs.Value(attrPage)
	navigation, _ := attrs.Value(attrNavigation)
	return fmt.Sprintf("%s %s %d %s", page.AsString(), navigation.AsString(), count, sum)
}

// round drops a float's noise beyond a microsecond, so sums of seconds compare as written.
func round(f float64) float64 {
	return float64(int64(f*1e6+0.5)) / 1e6
}
