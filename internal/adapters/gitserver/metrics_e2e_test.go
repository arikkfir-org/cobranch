package gitserver_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestMetrics(t *testing.T) {
	s := newStack(t)
	dir := clone(t, s.websiteURL(s.base, olivia))
	commit(t, dir, "README.md", "# Website\n")
	mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")

	// A second Git server, measured and with an empty cache, serves two clones: the first downloads the push's pack
	// once, and every other repository finds it in the cache.
	reader := sdkmetric.NewManualReader()
	s.meter = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	measured := httptest.NewServer(s.server(t.TempDir()).Handler())
	t.Cleanup(measured.Close)
	clone(t, s.websiteURL(measured.URL, gwen))
	clone(t, s.websiteURL(measured.URL, gwen))

	got := collect(t, reader)
	duration, size := got["cobranch.git.workspace.duration"], got["cobranch.git.workspace.size"]
	lookups, built := got["cobranch.git.pack_cache.lookups"], int64(0)
	for _, op := range []string{"info-refs", "upload-pack"} {
		if duration[op] < 2 {
			t.Errorf("built %d repositories for %s, want one per clone at least", duration[op], op)
		}
		if size[op] != duration[op] {
			t.Errorf("measured the disk of %d repositories for %s, want all %d built", size[op], op, duration[op])
		}
		built += duration[op]
	}
	if len(duration) != 2 {
		t.Errorf("built repositories for %v, want info-refs and upload-pack alone", duration)
	}
	if lookups["miss"] != 1 || lookups["hit"] != built-1 {
		t.Errorf("pack cache lookups = %v, want one miss and a hit for each of the other %d repositories", lookups, built-1)
	}
	if limit := got["cobranch.git.pack_cache.limit"][""]; limit != 1<<30 {
		t.Errorf("cobranch.git.pack_cache.limit = %d, want the cache's size", limit)
	}
	if usage := got["cobranch.git.pack_cache.usage"][""]; usage <= 0 {
		t.Errorf("cobranch.git.pack_cache.usage = %d, want the pack's bytes", usage)
	}
}

// collect reads reader's metrics: each metric's count of measurements, sum or value, by the value of its one
// attribute, or "" for a metric without one.
func collect(t *testing.T, reader sdkmetric.Reader) map[string]map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	got := map[string]map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			got[m.Name] = byAttribute(t, m)
		}
	}
	return got
}

// byAttribute is m's count of measurements, sum or value by the value of its one attribute.
func byAttribute(t *testing.T, m metricdata.Metrics) map[string]int64 {
	t.Helper()
	got := map[string]int64{}
	switch data := m.Data.(type) {
	case metricdata.Histogram[float64]:
		for _, dp := range data.DataPoints {
			got[only(dp.Attributes)] += int64(dp.Count) //nolint:gosec // a test's few measurements
		}
	case metricdata.Histogram[int64]:
		for _, dp := range data.DataPoints {
			got[only(dp.Attributes)] += int64(dp.Count) //nolint:gosec // a test's few measurements
		}
	case metricdata.Sum[int64]:
		for _, dp := range data.DataPoints {
			got[only(dp.Attributes)] += dp.Value
		}
	case metricdata.Gauge[int64]:
		for _, dp := range data.DataPoints {
			got[only(dp.Attributes)] = dp.Value
		}
	default:
		t.Fatalf("%s is %T, which collect doesn't read", m.Name, m.Data)
	}
	return got
}

// only is the value of the one attribute in set, or "" when it has none.
func only(set attribute.Set) string {
	for _, kv := range set.ToSlice() {
		return kv.Value.String()
	}
	return ""
}
