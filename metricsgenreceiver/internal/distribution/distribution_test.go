package distribution

import (
	"math"
	"math/rand"
	"testing"

	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/dp"
	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/expohistogen"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pmetric"
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

	t.Run("delta exponential histogram", func(t *testing.T) {
		// Create exponential histogram generator with builtin template
		expHistoGen, err := expohistogen.NewGenerator("builtin/exponential-histograms-low-frequency.ndjson")
		assert.NoError(t, err)
		assert.NotNil(t, expHistoGen)

		metric := pmetric.NewMetric()
		metric.SetName("test_exponential_histogram")
		metric.SetEmptyExponentialHistogram()
		metric.ExponentialHistogram().SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
		dp := metric.ExponentialHistogram().DataPoints().AppendEmpty()

		// Set initial values
		dp.SetScale(2)
		dp.SetZeroCount(5)
		dp.Positive().SetOffset(0)
		dp.Positive().BucketCounts().FromRaw([]uint64{10, 20, 30})
		dp.SetCount(65)

		initialCount := dp.Count()

		// Advance the exponential histogram
		AdvanceDataPoint(dp, r, metric, DefaultDistribution, expHistoGen)

		// Verify the exponential histogram was generated from the template
		assert.Greater(t, dp.Count(), uint64(0), "count should be positive")
		assert.Greater(t, dp.Positive().BucketCounts().Len(), 0, "should have positive buckets")
		// The data point should be replaced with values from the template
		assert.NotEqual(t, dp.Count(), initialCount, "count should change from the template")
	})
}

func TestRoundToPrecision(t *testing.T) {
	assert.Equal(t, 1.23, roundToPrecision(1.23456, 2))
	assert.Equal(t, 1.2346, roundToPrecision(1.23456, 4))
	assert.Equal(t, 1.0, roundToPrecision(1.23456, 0))
	assert.Equal(t, 0.0, roundToPrecision(0.004, 2))
	assert.Equal(t, 0.01, roundToPrecision(0.005, 2))
	assert.Equal(t, -1.23, roundToPrecision(-1.23456, 2))
	assert.Equal(t, -0.01, roundToPrecision(-0.005, 2))
	assert.Equal(t, -1.0, roundToPrecision(-1.23456, 0))
	assert.Equal(t, 1.5, roundToPrecision(1.5, 2))
	assert.Equal(t, 1.5, roundToPrecision(1.5, 4))
	assert.Equal(t, 100.0, roundToPrecision(100.0, 2))
	assert.Equal(t, 0.12, roundToPrecision(0.12, 3))
}

func TestGetPrecision(t *testing.T) {
	dist := DistributionCfg{
		Precision:          intPtr(2),
		PrecisionOverrides: map[string]int{"special_metric": 4},
	}

	assert.Equal(t, 2, *dist.GetPrecision("regular_metric"))
	assert.Equal(t, 4, *dist.GetPrecision("special_metric"))

	dist.Precision = nil
	assert.Nil(t, dist.GetPrecision("regular_metric"))
}

func TestPrecisionAppliedToDoubles(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	t.Run("default precision", func(t *testing.T) {
		dist := DistributionCfg{
			MedianMonotonicSum: 100,
			StdDevGaugePct:     0.01,
			StdDev:             1.0,
			Precision:          intPtr(2),
		}
		metric := pmetric.NewMetric()
		metric.SetName("test_gauge")
		metric.SetEmptyGauge()
		dp := metric.Gauge().DataPoints().AppendEmpty()
		dp.SetDoubleValue(0.5)

		for i := 0; i < 100; i++ {
			AdvanceDataPoint(dp, rng, metric, dist, nil)
			assert.Equal(t, math.Round(dp.DoubleValue()*100)/100, dp.DoubleValue())
		}
	})

	t.Run("per-metric override", func(t *testing.T) {
		dist := DistributionCfg{
			MedianMonotonicSum: 100,
			StdDevGaugePct:     0.01,
			StdDev:             1.0,
			Precision:          intPtr(2),
			PrecisionOverrides: map[string]int{"high_precision_gauge": 4},
		}
		metric := pmetric.NewMetric()
		metric.SetName("high_precision_gauge")
		metric.SetEmptyGauge()
		dp := metric.Gauge().DataPoints().AppendEmpty()
		dp.SetDoubleValue(0.5)

		for i := 0; i < 100; i++ {
			AdvanceDataPoint(dp, rng, metric, dist, nil)
			assert.Equal(t, math.Round(dp.DoubleValue()*10000)/10000, dp.DoubleValue())
		}
	})

	t.Run("no precision configured", func(t *testing.T) {
		metric := pmetric.NewMetric()
		metric.SetName("test_gauge")
		metric.SetEmptyGauge()
		dp := metric.Gauge().DataPoints().AppendEmpty()
		dp.SetDoubleValue(0.5)

		for i := 0; i < 100; i++ {
			AdvanceDataPoint(dp, rng, metric, DefaultDistribution, nil)
			if math.Round(dp.DoubleValue()*100)/100 != dp.DoubleValue() {
				return
			}
		}
		t.Fatal("expected at least one value with more than 2 decimal places")
	})
}

func intPtr(v int) *int { return &v }

func advanceDataPointNTimes(dp dp.DataPoint, r *rand.Rand, metric pmetric.Metric, n int) {
	for i := 0; i < n; i++ {
		AdvanceDataPoint(dp, r, metric, DefaultDistribution, nil)
	}
}
