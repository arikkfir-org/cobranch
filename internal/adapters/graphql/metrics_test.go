package graphql

import (
	"context"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestFieldDurations(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []string // "type field" of each duration recorded
	}{
		{name: "a query's field", query: `{ version }`, want: []string{"query version"}},
		{name: "a query's field beside its type's name, which nothing resolves", query: `{ version __typename }`, want: []string{"query version"}},
		{name: "a mutation's field, refused", query: `mutation { revokeSession(id: \"x\") }`, want: []string{"mutation revokeSession"}},
		{name: "a query that fails validation", query: `{ secrets }`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := sdkmetric.NewManualReader()
			h := NewHandler("v1.2.3", "", nil, nil, nil, nil, nil, WithMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))))
			req := httptest.NewRequest("POST", "/", strings.NewReader(`{"query":"`+tt.query+`"}`))
			req.Header.Set("Content-Type", "application/json")
			h.ServeHTTP(httptest.NewRecorder(), req)

			if got := fieldDurations(t, reader); !slices.Equal(got, tt.want) {
				t.Errorf("recorded %v, want %v", got, tt.want)
			}
		})
	}
}

// fieldDurations collects the root fields' durations from reader: "type field" of each, in order.
func fieldDurations(t *testing.T, reader sdkmetric.Reader) []string {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			h, ok := m.Data.(metricdata.Histogram[float64])
			if m.Name != "cobranch.graphql.field.duration" || !ok {
				t.Fatalf("recorded %s as %T, want cobranch.graphql.field.duration alone", m.Name, m.Data)
			}
			for _, dp := range h.DataPoints {
				typ, _ := dp.Attributes.Value(attrOperationType)
				field, _ := dp.Attributes.Value(attrField)
				got = append(got, typ.AsString()+" "+field.AsString())
			}
		}
	}
	slices.Sort(got)
	return got
}
