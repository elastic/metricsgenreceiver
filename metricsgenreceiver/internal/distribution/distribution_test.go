package distribution

import (
	"github.com/felixbarny/metricsgenreceiver/metricsgenreceiver/internal/dp"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"math/rand"
	"testing"
)

var r = rand.New(rand.NewSource(1))

func TestAdvanceDataPoint(t *testing.T) {
	t.Run("gauge double between 0 and 1", func(t *testing.T) {
		metric := pmetric.NewMetric()
		metric.SetName("test_gauge")
		metric.SetEmptyGauge()
		dp := metric.Gauge().DataPoints().AppendEmpty()
		dp.SetDoubleValue(0.0)

		advanceDataPointNTimes(dp, r, metric, 100)

		assert.True(t, dp.DoubleValue() >= 0 && dp.DoubleValue() <= 1)
	})

	t.Run("gauge double outside 0-1 range", func(t *testing.T) {
		metric := pmetric.NewMetric()
		metric.SetName("test_gauge")
		metric.SetEmptyGauge()
		dp := metric.Gauge().DataPoints().AppendEmpty()
		initialValue := 1.1
		dp.SetDoubleValue(initialValue)

		advanceDataPointNTimes(dp, r, metric, 100)

		assert.NotEqual(t, dp.DoubleValue(), initialValue)
		// Verify it didn't get trapped in 0-1 range
		assert.False(t, dp.DoubleValue() >= 0 && dp.DoubleValue() <= 1)
	})

	t.Run("gauge int", func(t *testing.T) {
		metric := pmetric.NewMetric()
		metric.SetName("test_gauge")
		metric.SetEmptyGauge()
		dp := metric.Gauge().DataPoints().AppendEmpty()
		initialValue := int64(100)
		dp.SetIntValue(initialValue)

		advanceDataPointNTimes(dp, r, metric, 10)

		assert.NotEqual(t, dp.IntValue(), initialValue)
		assert.True(t, dp.IntValue() > 0)
	})

	t.Run("monotonic cumulative sum", func(t *testing.T) {
		metric := pmetric.NewMetric()
		metric.SetName("test_sum")
		metric.SetEmptySum()
		metric.Sum().SetIsMonotonic(true)
		metric.Sum().SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
		dp := metric.Sum().DataPoints().AppendEmpty()
		initialValue := 100.0
		dp.SetDoubleValue(initialValue)

		advanceDataPointNTimes(dp, r, metric, 10)

		assert.Greater(t, dp.DoubleValue(), initialValue)
	})

	t.Run("monotonic delta sum", func(t *testing.T) {
		metric := pmetric.NewMetric()
		metric.SetName("test_sum")
		metric.SetEmptySum()
		metric.Sum().SetIsMonotonic(true)
		metric.Sum().SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
		dp := metric.Sum().DataPoints().AppendEmpty()
		dp.SetDoubleValue(100.0)

		advanceDataPointNTimes(dp, r, metric, 1)

		// For delta temporality, the value should be independent of the previous value
		assert.True(t, dp.DoubleValue() > 0)
	})

	t.Run("non-monotonic sum", func(t *testing.T) {
		metric := pmetric.NewMetric()
		metric.SetName("test_sum")
		metric.SetEmptySum()
		metric.Sum().SetIsMonotonic(false)
		dp := metric.Sum().DataPoints().AppendEmpty()
		initialValue := 100.0
		dp.SetDoubleValue(initialValue)

		advanceDataPointNTimes(dp, r, metric, 1)

		// For non-monotonic sums, the value can change in either direction
		assert.NotEqual(t, dp.DoubleValue(), initialValue)
	})

	t.Run("cumulative histogram", func(t *testing.T) {
		metric := pmetric.NewMetric()
		metric.SetName("test_histogram")
		metric.SetEmptyHistogram()
		metric.Histogram().SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
		dp := metric.Histogram().DataPoints().AppendEmpty()
		dp.BucketCounts().FromRaw([]uint64{1, 2, 3})
		dp.ExplicitBounds().FromRaw([]float64{10, 20})
		dp.SetCount(6)

		advanceDataPointNTimes(dp, r, metric, 100)

		assert.Equal(t, dp.BucketCounts().Len(), 3)
		assert.Greater(t, dp.Count(), uint64(0))
		assert.NotEqual(t, dp.Count(), 6)
	})

	t.Run("delta histogram", func(t *testing.T) {
		metric := pmetric.NewMetric()
		metric.SetName("test_histogram")
		metric.SetEmptyHistogram()
		metric.Histogram().SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
		dp := metric.Histogram().DataPoints().AppendEmpty()
		dp.BucketCounts().FromRaw([]uint64{1, 2, 3})
		dp.ExplicitBounds().FromRaw([]float64{10, 20})
		dp.SetCount(6)

		advanceDataPointNTimes(dp, r, metric, 100)

		assert.Equal(t, dp.BucketCounts().Len(), 3)
		assert.NotEqual(t, dp.Count(), 6)
	})
}

func advanceDataPointNTimes(dp dp.DataPoint, r *rand.Rand, metric pmetric.Metric, n int) {
	for i := 0; i < n; i++ {
		AdvanceDataPoint(dp, r, metric, DefaultDistribution)
	}
}
