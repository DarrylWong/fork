// Copyright 2022 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package schedulerlatency

import (
	"math"
	"runtime/metrics"
	"time"

	"github.com/cockroachdb/cockroach/pkg/util/metric"
	"github.com/cockroachdb/cockroach/pkg/util/syncutil"
	"github.com/gogo/protobuf/proto"
	prometheusgo "github.com/prometheus/client_model/go"
)

// runtimeHistogram is a histogram that's used to export histograms generated by
// the Go runtime; it's mutable and is updated in batches. The code here is
// adapted from [1][2]:
//
// [1]: github.com/prometheus/client_golang/blob/5b7e8b2e/prometheus/go_collector_latest.go
// [2]: github.com/prometheus/client_golang/blob/5b7e8b2e/prometheus/internal/go_runtime_metrics.go
type runtimeHistogram struct {
	metric.Metadata
	mu struct {
		syncutil.Mutex
		buckets []float64 // inclusive lower bounds, like runtime/metrics
		counts  []uint64
	}
	mult float64 // multiplier to apply to each bucket boundary, used when translating across units
}

var _ metric.Iterable = &runtimeHistogram{}
var _ metric.PrometheusExportable = &runtimeHistogram{}
var _ metric.WindowedHistogram = (*runtimeHistogram)(nil)

// newRuntimeHistogram creates a histogram with the given metadata configured
// with the given buckets. The buckets must be a strict subset of what this
// histogram is updated with and follow the same conventions as those in
// runtime/metrics.
func newRuntimeHistogram(metadata metric.Metadata, buckets []float64) *runtimeHistogram {
	// We need to remove -Inf values. runtime/metrics keeps them around.
	// But -Inf bucket should not be allowed for prometheus histograms.
	if buckets[0] == math.Inf(-1) {
		buckets = buckets[1:]
	}
	metadata.MetricType = prometheusgo.MetricType_HISTOGRAM
	h := &runtimeHistogram{
		Metadata: metadata,
		// Go runtime histograms as of go1.19 are always in seconds whereas
		// CRDB's histograms are in nanoseconds. Hardcode the conversion factor
		// between the two, use it when translating to the prometheus exportable
		// form (also used when writing to CRDB's internal TSDB).
		mult: float64(time.Second.Nanoseconds()),
	}
	h.mu.buckets = buckets
	// Because buckets follows runtime/metrics conventions, there's
	// one more value in the buckets list than there are buckets represented,
	// because in runtime/metrics, the bucket values represent boundaries,
	// and non-Inf boundaries are inclusive lower bounds for that bucket.
	h.mu.counts = make([]uint64, len(buckets)-1)
	return h
}

// update the histogram from a runtime/metrics histogram.
func (h *runtimeHistogram) update(his *metrics.Float64Histogram) {
	h.mu.Lock()
	defer h.mu.Unlock()
	counts, buckets := his.Counts, his.Buckets

	for i := range h.mu.counts {
		h.mu.counts[i] = 0 // clear buckets
	}
	var j int
	for i, count := range counts { // copy and reduce buckets
		h.mu.counts[j] += count
		if buckets[i+1] == h.mu.buckets[j+1] {
			j++
		}
	}
}

// write serializes the underlying histogram state into the form prometheus
// expects.
func (h *runtimeHistogram) write(out *prometheusgo.Metric) {
	h.mu.Lock()
	defer h.mu.Unlock()

	sum := float64(0)
	dtoBuckets := make([]*prometheusgo.Bucket, 0, len(h.mu.counts))
	totalCount := uint64(0)
	for i, count := range h.mu.counts {
		totalCount += count
		if count != 0 {
			// N.B. this computed sum is an underestimate since we're using the
			// lower bound of the bucket.
			sum += h.mu.buckets[i] * h.mult * float64(count)
		}

		// Skip the +Inf bucket, but only for the bucket list. It must still
		// count for sum and totalCount.
		if math.IsInf(h.mu.buckets[i+1]*h.mult, 1) {
			break
		}
		// Float64Histogram's upper bound is exclusive, so make it inclusive by
		// obtaining the next float64 value down, in order.
		upperBound := math.Nextafter(h.mu.buckets[i+1], h.mu.buckets[i]) * h.mult
		dtoBuckets = append(dtoBuckets, &prometheusgo.Bucket{
			CumulativeCount: proto.Uint64(totalCount),
			UpperBound:      proto.Float64(upperBound),
		})
	}
	out.Histogram = &prometheusgo.Histogram{
		Bucket:      dtoBuckets,
		SampleCount: proto.Uint64(totalCount),
		SampleSum:   proto.Float64(sum),
	}
}

// GetType is part of the PrometheusExportable interface.
func (h *runtimeHistogram) GetType() *prometheusgo.MetricType {
	return prometheusgo.MetricType_HISTOGRAM.Enum()
}

// ToPrometheusMetric is part of the PrometheusExportable interface.
func (h *runtimeHistogram) ToPrometheusMetric() *prometheusgo.Metric {
	m := &prometheusgo.Metric{}
	h.write(m)
	return m
}

// ToPrometheusMetricWindowed returns a filled-in prometheus metric of the
// right type for the current histogram window.
func (h *runtimeHistogram) ToPrometheusMetricWindowed() *prometheusgo.Metric {
	return h.ToPrometheusMetric()
}

// GetMetadata is part of the PrometheusExportable interface.
func (h *runtimeHistogram) GetMetadata() metric.Metadata {
	return h.Metadata
}

// Inspect is part of the Iterable interface.
func (h *runtimeHistogram) Inspect(f func(interface{})) { f(h) }

// Total implements the WindowedHistogram interface.
func (h *runtimeHistogram) Total(hist *prometheusgo.Metric) (int64, float64) {
	pHist := hist.Histogram
	return int64(pHist.GetSampleCount()), pHist.GetSampleSum()
}

// ValueAtQuantileWindowed implements the WindowedHistogram interface.
func (h *runtimeHistogram) ValueAtQuantileWindowed(q float64, window *prometheusgo.Metric) float64 {
	return metric.ValueAtQuantileWindowed(window.Histogram, q)
}

// Mean implements the WindowedHistogram interface.
func (h *runtimeHistogram) Mean(hist *prometheusgo.Metric) float64 {
	pHist := hist.Histogram
	return pHist.GetSampleSum() / float64(pHist.GetSampleCount())
}

// reBucketExpAndTrim takes a list of bucket boundaries (lower bound inclusive)
// and down samples the buckets to those a multiple of base apart. The end
// result is a roughly exponential (in many cases, perfectly exponential)
// bucketing scheme. It also trims the bucket range to the specified min and max
// values -- everything outside the range is merged into (-Inf, ..] and [..,
// +Inf) buckets. The following example shows how it works, lifted from
// testdata/histogram_buckets.
//
//		rebucket base=10 min=0ns max=100000h
//		----
//		bucket[  0] width=0s                 boundary=[-Inf, 0s)
//	    bucket[  1] width=1ns                boundary=[0s, 1ns)
//	    bucket[  2] width=9ns                boundary=[1ns, 10ns)
//	    bucket[  3] width=90ns               boundary=[10ns, 100ns)
//	    bucket[  4] width=924ns              boundary=[100ns, 1.024µs)
//	    bucket[  5] width=9.216µs            boundary=[1.024µs, 10.24µs)
//	    bucket[  6] width=92.16µs            boundary=[10.24µs, 102.4µs)
//	    bucket[  7] width=946.176µs          boundary=[102.4µs, 1.048576ms)
//	    bucket[  8] width=9.437184ms         boundary=[1.048576ms, 10.48576ms)
func reBucketExpAndTrim(buckets []float64, base, min, max float64) []float64 {
	// Re-bucket as powers of the given base.
	b := reBucketExp(buckets, base)

	// Merge all buckets greater than the max value into the +Inf bucket.
	for i := range b {
		if i == 0 {
			continue
		}
		if b[i-1] <= max {
			continue
		}

		// We're looking at the boundary after the first time we've crossed the
		// max limit. Since we expect recordings near the max value, we don't
		// want that bucket to end at +Inf, so we merge the bucket after.
		b[i] = math.Inf(1)
		b = b[:i+1]
		break
	}

	// Merge all buckets less than the min value into the -Inf bucket.
	j := 0
	for i := range b {
		if b[i] > min {
			j = i
			break
		}
	}
	// b[j] > min and is the lower-bound of the j-th bucket. The min must be
	// contained in the (j-1)-th bucket. We want to merge 0th bucket
	// until the (j-2)-th one.
	if j <= 2 {
		// Nothing to do (we either have one or no buckets to merge together).
	} else {
		// We want trim the bucket list to start at (j-2)-th bucket, so just
		// have one bucket before the one containing the min.
		b = b[j-2:]
		// b[0] now refers the lower bound of what was previously the (j-2)-th
		// bucket. We make it start at -Inf.
		b[0] = math.Inf(-1)
	}

	return b
}

// reBucketExp is like reBucketExpAndTrim but without the trimming logic.
func reBucketExp(buckets []float64, base float64) []float64 {
	bucket := buckets[0]
	var newBuckets []float64
	// We may see -Inf here, in which case, add it and continue the rebucketing
	// scheme from the next one it since we risk producing NaNs otherwise. We
	// need to preserve -Inf values to maintain runtime/metrics conventions
	if bucket == math.Inf(-1) {
		newBuckets = append(newBuckets, bucket)
		buckets = buckets[1:]
		bucket = buckets[0]
	}

	// From now on, bucket should always have a non-Inf value because Infs are
	// only ever at the ends of the bucket lists, so arithmetic operations on it
	// are non-NaN.
	for i := 1; i < len(buckets); i++ {
		// bucket is the lower bound of the lowest bucket that has not been
		// added to newBuckets. We will add it to newBuckets, but we wait to add
		// it until we find the next bucket that is >= bucket*base.

		if bucket >= 0 && buckets[i] < bucket*base {
			// The next bucket we want to include is at least bucket*base.
			continue
		} else if bucket < 0 && buckets[i] < bucket/base {
			// In this case the bucket we're targeting is negative, and since
			// we're ascending through buckets here, we need to divide to get
			// closer to zero exponentially.
			continue
		}
		newBuckets = append(newBuckets, bucket)
		bucket = buckets[i]
	}

	// The +Inf bucket will always be the last one, and we'll always
	// end up including it here.
	return append(newBuckets, bucket)
}
