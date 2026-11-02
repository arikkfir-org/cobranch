package rest

import (
	"context"
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"cobranch.dev/internal/system/telemetry"
)

// meterName is the instrumentation scope of the REST server's metrics.
const meterName = "cobranch.dev/internal/adapters/rest"

// The attributes of a page view: the Console's page and how the viewer came to it, each from a fixed list.
const (
	attrPage       = attribute.Key("cobranch.console.page")
	attrNavigation = attribute.Key("cobranch.console.navigation")
)

// maxPageViewsBody is the largest report of page views the API reads: 20 views take about 2.5 KiB.
const maxPageViewsBody = 16 << 10

var (
	// pageBuckets are the durations' buckets, in seconds, with the longer waits a page may take beside a route's.
	pageBuckets = append(append([]float64{}, telemetry.DurationBuckets...), 20, 30, 60)
	// readBuckets count the Git reads a page took.
	readBuckets = []float64{0, 1, 2, 3, 4, 6, 8, 12, 16, 32}
)

// Option changes how NewHandler serves.
type Option func(*options)

type options struct {
	meter metric.MeterProvider
}

// WithMeterProvider records the handler's metrics with mp instead of the process's meter provider.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(o *options) { o.meter = mp }
}

// pageMetrics are the instruments of the Console's page views, which the Console reports to the REST API since only
// the browser knows when a page has shown
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/monitoring.md, "A page view").
type pageMetrics struct {
	duration metric.Float64Histogram
	reads    metric.Int64Histogram
}

// newPageMetrics makes the page views' instruments from mp, or from the process's meter provider when it is nil.
func newPageMetrics(mp metric.MeterProvider) pageMetrics {
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	meter := mp.Meter(meterName)
	// OpenTelemetry refuses only an invalid name or unit, which these aren't, and answers a no-op instrument then.
	duration, _ := meter.Float64Histogram("cobranch.console.page.duration", metric.WithUnit("s"),
		metric.WithDescription("How long a Console page took to show, from the wait's start to the page drawn."),
		metric.WithExplicitBucketBoundaries(pageBuckets...))
	reads, _ := meter.Int64Histogram("cobranch.console.page.git.reads", metric.WithUnit("{read}"),
		metric.WithDescription("How many Git reads, each a throwaway repository on the Git server, a Console page's API calls took."),
		metric.WithExplicitBucketBoundaries(readBuckets...))
	return pageMetrics{duration: duration, reads: reads}
}

// ReportPageViews records each of the Console's page views, once every one of them is valid: a page and a navigation
// the API names, at most 20 views, none longer than ten minutes. It takes no credential, since the sign-in page reports
// too, and records nothing of who viewed what.
func (a api) ReportPageViews(ctx context.Context, req ReportPageViewsRequestObject) (ReportPageViewsResponseObject, error) {
	if req.Body == nil || len(req.Body.Views) == 0 || len(req.Body.Views) > 20 {
		return nil, refuse(http.StatusBadRequest, "request.invalid", "A report holds 1 to 20 page views.")
	}
	for i, v := range req.Body.Views {
		if !v.Page.Valid() || !v.Navigation.Valid() || v.DurationMs < 0 || v.DurationMs > 600000 || v.GitReads < 0 || v.GitReads > 1000 {
			return nil, refuse(http.StatusBadRequest, "request.invalid", fmt.Sprintf("Page view %d names a page or a navigation the API doesn't, or a time or a count out of range.", i))
		}
	}
	for _, v := range req.Body.Views {
		attrs := metric.WithAttributes(attrPage.String(string(v.Page)), attrNavigation.String(string(v.Navigation)))
		a.pages.duration.Record(ctx, float64(v.DurationMs)/1000, attrs)
		a.pages.reads.Record(ctx, int64(v.GitReads), attrs)
	}
	return ReportPageViews204Response{}, nil
}
