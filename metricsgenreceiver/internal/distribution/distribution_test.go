package distribution

import (
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
		AdvanceDataPoint(dp, r, metric, DefaultDistribution, expHistoGen, nil)

		// Verify the exponential histogram was generated from the template
		assert.Greater(t, dp.Count(), uint64(0), "count should be positive")
		assert.Greater(t, dp.Positive().BucketCounts().Len(), 0, "should have positive buckets")
		// The data point should be replaced with values from the template
		assert.NotEqual(t, dp.Count(), initialCount, "count should change from the template")
	})
}

func TestDecimalPlaces(t *testing.T) {
	tests := []struct {
		value    float64
		expected int
	}{
		{0.05, 2},
		{415808.07, 2},
		{54076.076, 3},
		{46855.811, 3},
		{0.12862568203441407, 17},
		{3.809659957885742, 15},
		{1.0, 0},
		{30.0, 0},
		{100.0, 0},
		{0.0, 0},
		{-30.0, 0},
		{-54076.076, 3},
		{-0.05, 2},
		{1.5, 1},
		{0.1234, 4},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, decimalPlaces(tt.value), "decimalPlaces(%v)", tt.value)
	}
}

func TestRoundToPrecision(t *testing.T) {
	tests := []struct {
		value    float64
		decimals int
		expected float64
	}{
		{1.23456, 2, 1.23},
		{1.23556, 2, 1.24},
		{54076.0764, 3, 54076.076},
		{0.128625, 4, 0.1286},
		{100.0, 2, 100.0},
		{0.0, 2, 0.0},
		{-1.23456, 2, -1.23},
		{-1.23556, 2, -1.24},
		{0.005, 2, 0.01},
		{0.004, 2, 0.0},
		{1.1, 5, 1.1},
		{42.0, 0, 42.0},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, roundToPrecision(tt.value, tt.decimals), "roundToPrecision(%v, %d)", tt.value, tt.decimals)
	}
}

func TestInferPrecision(t *testing.T) {
	metrics := pmetric.NewMetrics()
	rm := metrics.ResourceMetrics().AppendEmpty()
	sm := rm.ScopeMetrics().AppendEmpty()

	m1 := sm.Metrics().AppendEmpty()
	m1.SetName("system.cpu.time")
	m1.SetEmptySum()
	m1.Sum().DataPoints().AppendEmpty().SetDoubleValue(415808.07)

	m2 := sm.Metrics().AppendEmpty()
	m2.SetName("system.disk.io_time")
	m2.SetEmptySum()
	m2.Sum().DataPoints().AppendEmpty().SetDoubleValue(54076.076)

	m3 := sm.Metrics().AppendEmpty()
	m3.SetName("system.cpu.load_average.1m")
	m3.SetEmptyGauge()
	m3.Gauge().DataPoints().AppendEmpty().SetDoubleValue(0)

	m4 := sm.Metrics().AppendEmpty()
	m4.SetName("system.network.io")
	m4.SetEmptySum()
	m4.Sum().DataPoints().AppendEmpty().SetIntValue(12345)

	m5 := sm.Metrics().AppendEmpty()
	m5.SetName("system.fs.inodes.usage")
	m5.SetEmptySum()
	m5.Sum().DataPoints().AppendEmpty().SetDoubleValue(30.0)
	m5.Sum().DataPoints().AppendEmpty().SetDoubleValue(1.0)

	precision := InferPrecision(&metrics)

	assert.Equal(t, 2, precision["system.cpu.time"])
	assert.Equal(t, 3, precision["system.disk.io_time"])
	assert.Equal(t, 0, precision["system.fs.inodes.usage"])
	_, hasLoadAvg := precision["system.cpu.load_average.1m"]
	assert.False(t, hasLoadAvg)
	_, hasInt := precision["system.network.io"]
	assert.False(t, hasInt)
}

func TestInferPrecisionUsesMaxAcrossDataPoints(t *testing.T) {
	metrics := pmetric.NewMetrics()
	rm := metrics.ResourceMetrics().AppendEmpty()
	sm := rm.ScopeMetrics().AppendEmpty()

	m := sm.Metrics().AppendEmpty()
	m.SetName("test_metric")
	m.SetEmptyGauge()
	m.Gauge().DataPoints().AppendEmpty().SetDoubleValue(1.5)
	m.Gauge().DataPoints().AppendEmpty().SetDoubleValue(2.123)

	precision := InferPrecision(&metrics)
	assert.Equal(t, 3, precision["test_metric"])
}

func TestAdvanceDataPointWithPrecision(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	metric := pmetric.NewMetric()
	metric.SetName("system.cpu.time")
	metric.SetEmptySum()
	metric.Sum().SetIsMonotonic(true)
	metric.Sum().SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	d := metric.Sum().DataPoints().AppendEmpty()
	d.SetDoubleValue(100.0)

	precision := map[string]int{"system.cpu.time": 2}
	for i := 0; i < 100; i++ {
		AdvanceDataPoint(d, rng, metric, DefaultDistribution, nil, precision)
		assert.Equal(t, roundToPrecision(d.DoubleValue(), 2), d.DoubleValue())
	}
}

func TestAdvanceDataPointWithZeroPrecision(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	metric := pmetric.NewMetric()
	metric.SetName("system.fs.inodes.usage")
	metric.SetEmptySum()
	metric.Sum().SetIsMonotonic(true)
	metric.Sum().SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	d := metric.Sum().DataPoints().AppendEmpty()
	d.SetDoubleValue(30.0)

	precision := map[string]int{"system.fs.inodes.usage": 0}
	for i := 0; i < 100; i++ {
		AdvanceDataPoint(d, rng, metric, DefaultDistribution, nil, precision)
		assert.Equal(t, roundToPrecision(d.DoubleValue(), 0), d.DoubleValue())
	}
}

func advanceDataPointNTimes(dp dp.DataPoint, r *rand.Rand, metric pmetric.Metric, n int) {
	for i := 0; i < n; i++ {
		AdvanceDataPoint(dp, r, metric, DefaultDistribution, nil, nil)
	}
}
