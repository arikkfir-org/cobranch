package graphql

import (
	"context"
	"time"

	gql "github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"cobranch.dev/internal/system/telemetry"
)

// meterName is the instrumentation scope of the GraphQL server's metrics.
const meterName = "cobranch.dev/internal/adapters/graphql"

// The attributes of a root field's duration: the operation's type and the field's name, both the schema's.
const (
	attrOperationType = attribute.Key("graphql.operation.type")
	attrField         = attribute.Key("cobranch.graphql.field")
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

// fieldTimer records how long each root field of a query or a mutation takes, with everything beneath it:
// cobranch.graphql.field.duration, by the operation's type and the field's name. The schema bounds both, where an
// operation's name is its client's to choose. A subscription's field streams for as long as its client listens, so
// it isn't timed.
type fieldTimer struct {
	duration metric.Float64Histogram
}

var (
	_ gql.HandlerExtension     = fieldTimer{}
	_ gql.RootFieldInterceptor = fieldTimer{}
)

// newFieldTimer makes the root fields' instrument from mp, or from the process's meter provider when it is nil.
func newFieldTimer(mp metric.MeterProvider) fieldTimer {
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	// OpenTelemetry refuses only an invalid name or unit, which these aren't, and answers a no-op instrument then.
	duration, _ := mp.Meter(meterName).Float64Histogram("cobranch.graphql.field.duration", metric.WithUnit("s"),
		metric.WithDescription("How long a root field of a GraphQL query or mutation took, with everything beneath it."),
		metric.WithExplicitBucketBoundaries(telemetry.DurationBuckets...))
	return fieldTimer{duration: duration}
}

func (fieldTimer) ExtensionName() string { return "FieldTimer" }

func (fieldTimer) Validate(gql.ExecutableSchema) error { return nil }

func (t fieldTimer) InterceptRootField(ctx context.Context, next gql.RootResolver) gql.Marshaler {
	rc := gql.GetRootFieldContext(ctx)
	if rc == nil || !gql.HasOperationContext(ctx) {
		return next(ctx)
	}
	op := gql.GetOperationContext(ctx).Operation
	if op == nil || (op.Operation != ast.Query && op.Operation != ast.Mutation) {
		return next(ctx)
	}
	started := time.Now()
	result := next(ctx)
	t.duration.Record(ctx, time.Since(started).Seconds(), metric.WithAttributes(
		attrOperationType.String(string(op.Operation)), attrField.String(rc.Field.Name)))
	return result
}
