package gitserver

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"cobranch.dev/internal/system/telemetry"
)

// meterName is the instrumentation scope of the Git server's metrics.
const meterName = "cobranch.dev/internal/adapters/gitserver"

// The attributes of the Git server's metrics, each of a few values.
const (
	// attrOperation is what a throwaway repository served: an internal call's read or a merge, an HTTPS
	// advertisement, a fetch's pack or a push.
	attrOperation = attribute.Key("cobranch.git.operation")
	// attrCacheResult is whether the pack cache held a pack a repository linked: hit or miss.
	attrCacheResult = attribute.Key("cobranch.git.pack_cache.result")
)

// The operations a throwaway repository serves, beside the internal call's reads (tree, blob, raw and the rest), which
// name themselves.
const (
	opMerge      = "merge"
	opInfoRefs   = "info-refs"
	opUploadPack = "upload-pack"
	opRecvPack   = "receive-pack"
)

// metrics are the Git server's instruments: what each throwaway repository costs, and how the pack cache does
// (https://github.com/arikkfir-org/cobranch/blob/main/docs/cobranch/designs/monitoring.md).
type metrics struct {
	workspaceDuration metric.Float64Histogram
	workspaceSize     metric.Int64Histogram
	cacheLookups      metric.Int64Counter
}

// newMetrics makes the Git server's instruments from mp, or from the process's meter provider when it is nil, and
// observes c's usage and limit.
func newMetrics(mp metric.MeterProvider, c *cache) (*metrics, error) {
	if mp == nil {
		mp = otel.GetMeterProvider()
	}
	meter := mp.Meter(meterName)
	var m metrics
	var err, e error
	m.workspaceDuration, e = meter.Float64Histogram("cobranch.git.workspace.duration", metric.WithUnit("s"),
		metric.WithDescription("How long a throwaway repository took to build: its snapshot read, its layout and its packs linked."),
		metric.WithExplicitBucketBoundaries(telemetry.DurationBuckets...))
	err = errors.Join(err, e)
	m.workspaceSize, e = meter.Int64Histogram("cobranch.git.workspace.size", metric.WithUnit("By"),
		metric.WithDescription("The disk a throwaway repository took beyond the pack cache: the bytes of its files that nothing else linked."),
		metric.WithExplicitBucketBoundaries(telemetry.SizeBuckets...))
	err = errors.Join(err, e)
	m.cacheLookups, e = meter.Int64Counter("cobranch.git.pack_cache.lookups", metric.WithUnit("{lookup}"),
		metric.WithDescription("Each pack a throwaway repository linked: a hit when the pack cache held it, a miss when it was downloaded."))
	err = errors.Join(err, e)
	usage, e := meter.Int64ObservableGauge("cobranch.git.pack_cache.usage", metric.WithUnit("By"),
		metric.WithDescription("The bytes of packs the pack cache holds."))
	err = errors.Join(err, e)
	limit, e := meter.Int64ObservableGauge("cobranch.git.pack_cache.limit", metric.WithUnit("By"),
		metric.WithDescription("The bytes of packs the pack cache keeps before it evicts the least recently used repositories'."))
	err = errors.Join(err, e)
	if err != nil {
		return nil, fmt.Errorf("making the Git server's instruments: %w", err)
	}
	if _, err := meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		o.ObserveInt64(usage, c.usage())
		o.ObserveInt64(limit, c.limit)
		return nil
	}, usage, limit); err != nil {
		return nil, fmt.Errorf("observing the pack cache: %w", err)
	}
	return &m, nil
}

// built records, unless m is nil, that a throwaway repository for op took since started to build.
func (m *metrics) built(ctx context.Context, op string, started time.Time) {
	if m == nil {
		return
	}
	m.workspaceDuration.Record(ctx, time.Since(started).Seconds(), metric.WithAttributes(attrOperation.String(op)))
}

// removed records, unless m is nil, the disk the throwaway repository for op at dir took, just before it goes.
func (m *metrics) removed(ctx context.Context, op, dir string) {
	if m == nil {
		return
	}
	m.workspaceSize.Record(ctx, ownBytes(dir), metric.WithAttributes(attrOperation.String(op)))
}

// lookedUp records, unless m is nil, a lookup of a pack in the cache, which held it or didn't.
func (m *metrics) lookedUp(ctx context.Context, hit bool) {
	if m == nil {
		return
	}
	result := "miss"
	if hit {
		result = "hit"
	}
	m.cacheLookups.Add(ctx, 1, metric.WithAttributes(attrCacheResult.String(result)))
}

// ownBytes is how many bytes the files under dir hold that no other path links: a pack the cache still holds has a
// second link, so it costs dir nothing. A file it can't read counts as nothing: the repository goes either way.
func ownBytes(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error { // what it couldn't walk counts as nothing
		if err != nil || !d.Type().IsRegular() {
			return nil //nolint:nilerr // see above
		}
		info, err := d.Info()
		if err != nil || links(info) > 1 {
			return nil //nolint:nilerr // see above
		}
		total += info.Size()
		return nil
	})
	return total
}
