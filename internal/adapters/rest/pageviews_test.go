package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
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
		{name: "the sign-in page", body: views(view("sign-in", "load", 300, 0)), wantStatus: 204,
			want: []string{"sign-in load 1 0.3", "sign-in load 1 0"}},
		{name: "an organization's issues", body: views(view("organization.issues", "route", 200, 0)), wantStatus: 204,
			want: []string{"organization.issues route 1 0.2", "organization.issues route 1 0"}},
		{name: "the same page twice", body: views(view("organization", "route", 100, 0), view("organization", "route", 300, 0)), wantStatus: 204,
			want: []string{"organization route 2 0.4", "organization route 2 0"}},
		{name: "twenty views", body: views(slices.Repeat([]string{view("welcome", "route", 10, 0)}, 20)...), wantStatus: 204,
			want: []string{"welcome route 20 0.2", "welcome route 20 0"}},
		{name: "no views", body: views(), wantStatus: 400},
		{name: "too many views", body: views(slices.Repeat([]string{view("welcome", "route", 10, 0)}, 21)...), wantStatus: 400},
		{name: "a page the Console lacks", body: views(view("welcome", "route", 10, 0), view("admin", "route", 10, 0)), wantStatus: 400},
		{name: "another navigation", body: views(view("welcome", "prefetch", 10, 0)), wantStatus: 400},
		{name: "a negative time", body: views(view("welcome", "route", -1, 0)), wantStatus: 400},
		{name: "longer than ten minutes", body: views(view("welcome", "route", 600001, 0)), wantStatus: 400},
		{name: "too many reads", body: views(view("welcome", "route", 10, 1001)), wantStatus: 400},
		{name: "a body larger than any report", body: views(view("welcome", "route", 10, 0) + strings.Repeat(" ", 16<<10)), wantStatus: 400},
		{name: "not JSON", body: `views`, wantStatus: 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := sdkmetric.NewManualReader()
			h := NewHandler("v1.2.3", nil, nil, nil, nil, nil, WithMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))))
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
