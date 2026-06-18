package distribution

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/expohistogen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func applyVariation(v pmetric.NumberDataPoint, instanceID int, metric pmetric.Metric, hints map[string]MetricGenerationHint) {
	applyVariationWithOptions(v, instanceID, metric, hints, InstanceVariationOptions{})
}

func applyVariationWithOptions(v pmetric.NumberDataPoint, instanceID int, metric pmetric.Metric, hints map[string]MetricGenerationHint, opts InstanceVariationOptions) {
	ApplyInstanceVariation(v, instanceID, IdentityHash(metric.Name(), v.Attributes()), metric, hints, nil, opts)
}

func TestInstanceVariationIsDeterministic(t *testing.T) {
	hints := map[string]MetricGenerationHint{
		"system.cpu.utilization": {Class: GenerationHintSlowGauge},
	}

	metricA, dpA := newGaugeDoubleMetric("system.cpu.utilization", 0.55)
	dpA.Attributes().PutStr("cpu", "0")
	dpA.Attributes().PutStr("state", "user")
	applyVariation(dpA, 7, metricA, hints)

	// Same instance + metric + attrs (in different insertion order) → same output.
	metricB, dpB := newGaugeDoubleMetric("system.cpu.utilization", 0.55)
	dpB.Attributes().PutStr("state", "user")
	dpB.Attributes().PutStr("cpu", "0")
	applyVariation(dpB, 7, metricB, hints)

	// Different instance ID → different output.
	metricC, dpC := newGaugeDoubleMetric("system.cpu.utilization", 0.55)
	dpC.Attributes().PutStr("cpu", "0")
	dpC.Attributes().PutStr("state", "user")
	applyVariation(dpC, 8, metricC, hints)

	assert.Equal(t, dpA.DoubleValue(), dpB.DoubleValue())
	assert.NotEqual(t, dpA.DoubleValue(), dpC.DoubleValue())
}

func TestCurrentCountInstanceVariationKeepsCountsIntegralAndNonNegative(t *testing.T) {
	hints := map[string]MetricGenerationHint{
		"test.current_count": {Class: GenerationHintCurrentCount},
	}

	metricA, dpA := newGaugeIntMetric("test.current_count", 10)
	dpA.Attributes().PutStr("queue", "ingest")
	applyVariation(dpA, 0, metricA, hints)

	metricB, dpB := newGaugeIntMetric("test.current_count", 10)
	dpB.Attributes().PutStr("queue", "ingest")
	applyVariation(dpB, 1, metricB, hints)

	assert.NotEqual(t, dpA.IntValue(), dpB.IntValue())

	for _, instanceID := range []int{0, 1, 2, 7, 42} {
		metric, dp := newGaugeIntMetric("test.current_count", 1)
		dp.Attributes().PutStr("queue", "ingest")
		applyVariation(dp, instanceID, metric, hints)
		assert.GreaterOrEqual(t, dp.IntValue(), int64(0))
	}
}

func TestCurrentCountInstanceVariationChangesOverTimeWithoutState(t *testing.T) {
	start := time.Unix(1000, 0)
	opts := InstanceVariationOptions{
		StartTime: start,
		Interval:  30 * time.Second,
	}
	hints := map[string]MetricGenerationHint{
		"test.current_count": {Class: GenerationHintCurrentCount},
	}

	metricA, dpA := newGaugeIntMetric("test.current_count", 10)
	dpA.Attributes().PutStr("queue", "ingest")
	opts.Timestamp = start
	applyVariationWithOptions(dpA, 7, metricA, hints, opts)

	changed := false
	for i := 1; i <= 20; i++ {
		metricB, dpB := newGaugeIntMetric("test.current_count", 10)
		dpB.Attributes().PutStr("queue", "ingest")
		opts.Timestamp = start.Add(time.Duration(i) * opts.Interval)
		applyVariationWithOptions(dpB, 7, metricB, hints, opts)
		changed = changed || dpA.IntValue() != dpB.IntValue()
	}

	assert.True(t, changed)
}

func TestSlowGaugeInstanceVariationKeepsUtilizationBounded(t *testing.T) {
	hints := map[string]MetricGenerationHint{
		"system.cpu.utilization": {Class: GenerationHintSlowGauge},
	}
	for instanceID := 0; instanceID < 32; instanceID++ {
		metric, dp := newGaugeDoubleMetric("system.cpu.utilization", 0.99)
		dp.Attributes().PutStr("cpu", "0")
		applyVariation(dp, instanceID, metric, hints)
		assert.GreaterOrEqual(t, dp.DoubleValue(), 0.0)
		assert.LessOrEqual(t, dp.DoubleValue(), 1.0)
	}
}

func TestUnitIntervalGaugeVariationBouncesAtBounds(t *testing.T) {
	hints := map[string]MetricGenerationHint{
		"system.cpu.utilization": {Class: GenerationHintSlowGauge},
	}

	for instanceID := 0; instanceID < 64; instanceID++ {
		metric, dp := newGaugeDoubleMetric("system.cpu.utilization", 0.99)
		dp.Attributes().PutStr("cpu", "0")
		applyVariation(dp, instanceID, metric, hints)
		assert.GreaterOrEqual(t, dp.DoubleValue(), 0.0)
		assert.LessOrEqual(t, dp.DoubleValue(), 1.0)
	}
	assert.InDelta(t, 0.9, bounceBetweenZeroAndOne(1.1), 0.000001)
}

func TestGaugeInstanceVariationChangesOverTimeWithoutState(t *testing.T) {
	start := time.Unix(1000, 0)
	opts := InstanceVariationOptions{
		StartTime: start,
		Interval:  30 * time.Second,
	}

	metricA, dpA := newGaugeDoubleMetric("test.unhinted", 100)
	dpA.Attributes().PutStr("state", "used")
	opts.Timestamp = start
	applyVariationWithOptions(dpA, 7, metricA, nil, opts)

	metricB, dpB := newGaugeDoubleMetric("test.unhinted", 100)
	dpB.Attributes().PutStr("state", "used")
	opts.Timestamp = start.Add(5 * time.Minute)
	applyVariationWithOptions(dpB, 7, metricB, nil, opts)

	assert.NotEqual(t, dpA.DoubleValue(), dpB.DoubleValue())
}

func TestNonUnitGaugeInstanceVariationDoesNotScaleBelowBaseline(t *testing.T) {
	opts := InstanceVariationOptions{
		Timestamp: time.Unix(1000, 0),
		Interval:  30 * time.Second,
	}

	for instanceID := 0; instanceID < 128; instanceID++ {
		metric, dp := newGaugeDoubleMetric("test.unhinted", 1.01)
		applyVariationWithOptions(dp, instanceID, metric, nil, opts)
		assert.GreaterOrEqual(t, dp.DoubleValue(), 1.01)
	}
}

func TestCounterInstanceVariationKeepsCountersNonNegative(t *testing.T) {
	hints := map[string]MetricGenerationHint{
		"test.steady_counter": {Class: GenerationHintSteadyCounter},
		"test.sparse_counter": {Class: GenerationHintSparseCounter},
	}

	for _, instanceID := range []int{0, 1, 2, 7, 42} {
		steadyMetric, steadyDP := newCumulativeDoubleSumMetric("test.steady_counter", 100)
		steadyDP.Attributes().PutStr("device", "sda")
		applyVariation(steadyDP, instanceID, steadyMetric, hints)
		assert.GreaterOrEqual(t, steadyDP.DoubleValue(), 0.0)

		sparseMetric, sparseDP := newCumulativeDoubleSumMetric("test.sparse_counter", 5)
		sparseDP.Attributes().PutStr("device", "sda")
		applyVariation(sparseDP, instanceID, sparseMetric, hints)
		assert.GreaterOrEqual(t, sparseDP.DoubleValue(), 0.0)
	}
}

func TestCumulativeCounterInstanceVariationAddsElapsedRate(t *testing.T) {
	start := time.Unix(1000, 0)
	opts := InstanceVariationOptions{
		StartTime:        start,
		Interval:         30 * time.Second,
		CounterBaseDelta: 100,
	}
	hints := map[string]MetricGenerationHint{
		"test.steady_counter": {Class: GenerationHintSteadyCounter},
	}

	metricA, dpA := newCumulativeDoubleSumMetric("test.steady_counter", 1000)
	dpA.Attributes().PutStr("device", "sda")
	opts.Timestamp = start
	applyVariationWithOptions(dpA, 7, metricA, hints, opts)

	metricB, dpB := newCumulativeDoubleSumMetric("test.steady_counter", 1000)
	dpB.Attributes().PutStr("device", "sda")
	opts.Timestamp = start.Add(5 * time.Minute)
	applyVariationWithOptions(dpB, 7, metricB, hints, opts)

	assert.Greater(t, dpB.DoubleValue(), dpA.DoubleValue())
}

func TestCumulativeCounterExtraRateVariesWithoutDecreasingLargeSeed(t *testing.T) {
	start := time.Unix(1000, 0)
	opts := InstanceVariationOptions{
		StartTime:        start,
		Interval:         30 * time.Second,
		CounterBaseDelta: 100,
	}
	hints := map[string]MetricGenerationHint{
		"test.steady_counter": {Class: GenerationHintSteadyCounter},
	}

	prev := variedCumulativeCounterValue(t, "test.steady_counter", 246011864, 7, start, opts, hints)
	minDelta := math.Inf(1)
	maxDelta := math.Inf(-1)
	for i := 1; i <= 100; i++ {
		current := variedCumulativeCounterValue(t, "test.steady_counter", 246011864+float64(100*i), 7, start.Add(time.Duration(i)*opts.Interval), opts, hints)
		delta := current - prev
		assert.Greater(t, delta, 0.0)
		minDelta = min(minDelta, delta)
		maxDelta = max(maxDelta, delta)
		prev = current
	}

	assert.Greater(t, maxDelta-minDelta, 1.0)
}

func TestMultiplierVariationKeepsZeroFlat(t *testing.T) {
	hints := map[string]MetricGenerationHint{
		"test.sparse_counter": {Class: GenerationHintSparseCounter},
	}
	metric, dp := newCumulativeDoubleSumMetric("test.sparse_counter", 0)
	dp.Attributes().PutStr("device", "sda")

	applyVariation(dp, 99, metric, hints)

	assert.Equal(t, 0.0, dp.DoubleValue())
}

func TestNoOpInstanceVariationHintsRemainSynchronized(t *testing.T) {
	tests := []struct {
		name  string
		class GenerationHintClass
		build func() (pmetric.Metric, pmetric.NumberDataPoint)
		read  func(pmetric.NumberDataPoint) any
	}{
		{
			name:  "constant",
			class: GenerationHintConstant,
			build: func() (pmetric.Metric, pmetric.NumberDataPoint) {
				return newGaugeIntMetric("test.constant", 10)
			},
			read: func(dp pmetric.NumberDataPoint) any { return dp.IntValue() },
		},
		{
			name:  "clock",
			class: GenerationHintClock,
			build: func() (pmetric.Metric, pmetric.NumberDataPoint) {
				return newGaugeDoubleMetric("test.clock", 1000)
			},
			read: func(dp pmetric.NumberDataPoint) any { return dp.DoubleValue() },
		},
		{
			name:  "stable_binary",
			class: GenerationHintStableBinary,
			build: func() (pmetric.Metric, pmetric.NumberDataPoint) {
				return newGaugeIntMetric("test.stable_binary", 1)
			},
			read: func(dp pmetric.NumberDataPoint) any { return dp.IntValue() },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hints := map[string]MetricGenerationHint{}
			metricA, dpA := tc.build()
			metricB, dpB := tc.build()
			hints[metricA.Name()] = MetricGenerationHint{Class: tc.class}

			expected := tc.read(dpA)
			applyVariation(dpA, 0, metricA, hints)
			applyVariation(dpB, 1, metricB, hints)

			assert.Equal(t, expected, tc.read(dpA))
			assert.Equal(t, expected, tc.read(dpB))
		})
	}
}

func TestDefaultInstanceVariationForUnhintedMetrics(t *testing.T) {
	metricA, dpA := newGaugeDoubleMetric("test.unhinted", 50)
	metricB, dpB := newGaugeDoubleMetric("test.unhinted", 50)

	applyVariation(dpA, 0, metricA, nil)
	applyVariation(dpB, 1, metricB, nil)

	assert.NotEqual(t, dpA.DoubleValue(), dpB.DoubleValue())
}

func newGaugeDoubleMetric(name string, value float64) (pmetric.Metric, pmetric.NumberDataPoint) {
	metric := pmetric.NewMetric()
	metric.SetName(name)
	metric.SetEmptyGauge()
	dp := metric.Gauge().DataPoints().AppendEmpty()
	dp.SetDoubleValue(value)
	return metric, dp
}

func variedCumulativeCounterValue(t *testing.T, name string, value float64, instanceID int, timestamp time.Time, opts InstanceVariationOptions, hints map[string]MetricGenerationHint) float64 {
	t.Helper()
	metric, dp := newCumulativeDoubleSumMetric(name, value)
	dp.Attributes().PutStr("device", "sda")
	opts.Timestamp = timestamp
	applyVariationWithOptions(dp, instanceID, metric, hints, opts)
	return dp.DoubleValue()
}

func newGaugeIntMetric(name string, value int64) (pmetric.Metric, pmetric.NumberDataPoint) {
	metric := pmetric.NewMetric()
	metric.SetName(name)
	metric.SetEmptyGauge()
	dp := metric.Gauge().DataPoints().AppendEmpty()
	dp.SetIntValue(value)
	return metric, dp
}

func newCumulativeDoubleSumMetric(name string, value float64) (pmetric.Metric, pmetric.NumberDataPoint) {
	metric := pmetric.NewMetric()
	metric.SetName(name)
	metric.SetEmptySum()
	metric.Sum().SetIsMonotonic(true)
	metric.Sum().SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
	dp := metric.Sum().DataPoints().AppendEmpty()
	dp.SetDoubleValue(value)
	return metric, dp
}

func newExpoHistoDataPoint() pmetric.ExponentialHistogramDataPoint {
	dp := pmetric.NewExponentialHistogramDataPoint()
	dp.SetScale(4)
	dp.SetCount(100)
	dp.SetSum(50.0)
	dp.SetMin(0.1)
	dp.SetMax(2.0)
	dp.SetZeroCount(0)
	dp.Positive().SetOffset(10)
	dp.Positive().BucketCounts().Append(5, 20, 50, 20, 5)
	return dp
}

func TestExpoHistoVariationIsDeterministic(t *testing.T) {
	dpA := newExpoHistoDataPoint()
	dpB := newExpoHistoDataPoint()
	dpC := newExpoHistoDataPoint()

	hash := IdentityHash("test.expo", dpA.Attributes())

	ApplyExponentialHistogramVariation(dpA, 7, hash)
	ApplyExponentialHistogramVariation(dpB, 7, hash)
	ApplyExponentialHistogramVariation(dpC, 8, hash)

	assert.Equal(t, dpA.Count(), dpB.Count(), "same instance should produce same count")
	assert.Equal(t, dpA.Sum(), dpB.Sum(), "same instance should produce same sum")
	assert.NotEqual(t, dpA.Count(), dpC.Count(), "different instances should produce different counts")
}

func TestExpoHistoVariationVariesAcrossInstances(t *testing.T) {
	seenCounts := make(map[uint64]bool)
	hash := IdentityHash("test.expo", pmetric.NewExponentialHistogramDataPoint().Attributes())

	for instanceID := 0; instanceID < 20; instanceID++ {
		dp := newExpoHistoDataPoint()
		ApplyExponentialHistogramVariation(dp, instanceID, hash)
		seenCounts[dp.Count()] = true
	}

	assert.Greater(t, len(seenCounts), 5, "should produce diverse counts across instances")
}

func TestExpoHistoVariationPreservesNonEmptyBuckets(t *testing.T) {
	hash := IdentityHash("test.expo", pmetric.NewExponentialHistogramDataPoint().Attributes())

	for instanceID := 0; instanceID < 50; instanceID++ {
		dp := newExpoHistoDataPoint()
		ApplyExponentialHistogramVariation(dp, instanceID, hash)

		assert.Greater(t, dp.Count(), uint64(0), "varied histogram should never be empty for instance %d", instanceID)
		assert.Greater(t, dp.Positive().BucketCounts().Len(), 0, "should have positive buckets for instance %d", instanceID)
	}
}

func TestExpoHistoVariationPreservesMinMax(t *testing.T) {
	hash := IdentityHash("test.expo", pmetric.NewExponentialHistogramDataPoint().Attributes())

	for instanceID := 0; instanceID < 20; instanceID++ {
		dp := newExpoHistoDataPoint()
		origMin := dp.Min()
		origMax := dp.Max()
		ApplyExponentialHistogramVariation(dp, instanceID, hash)

		assert.Equal(t, origMin, dp.Min(), "min should be preserved for instance %d", instanceID)
		assert.Equal(t, origMax, dp.Max(), "max should be preserved for instance %d", instanceID)
		assert.Greater(t, dp.Sum(), 0.0, "sum should be positive for instance %d", instanceID)
	}
}

func TestExpoHistoVariationBucketCountsMatchTotal(t *testing.T) {
	hash := IdentityHash("test.expo", pmetric.NewExponentialHistogramDataPoint().Attributes())

	for instanceID := 0; instanceID < 20; instanceID++ {
		dp := newExpoHistoDataPoint()
		ApplyExponentialHistogramVariation(dp, instanceID, hash)

		var total uint64
		for i := 0; i < dp.Positive().BucketCounts().Len(); i++ {
			total += dp.Positive().BucketCounts().At(i)
		}
		for i := 0; i < dp.Negative().BucketCounts().Len(); i++ {
			total += dp.Negative().BucketCounts().At(i)
		}
		total += dp.ZeroCount()

		assert.Equal(t, dp.Count(), total, "count should match bucket sum for instance %d", instanceID)
	}
}

func TestExpoHistoVariationPreservesMonotonicity(t *testing.T) {
	gen, err := expohistogen.NewGenerator("builtin/exponential-histograms-high-frequency.ndjson")
	require.NoError(t, err)

	rng := rand.New(rand.NewSource(42))
	hash := IdentityHash("test.expo", pmetric.NewExponentialHistogramDataPoint().Attributes())

	template := pmetric.NewExponentialHistogramDataPoint()

	for instanceID := 0; instanceID < 5; instanceID++ {
		var prevCount uint64
		var prevSum float64

		for interval := 0; interval < 50; interval++ {
			gen.MergeInto(rng, template)

			instance := pmetric.NewExponentialHistogramDataPoint()
			template.CopyTo(instance)
			ApplyExponentialHistogramVariation(instance, instanceID, hash)

			assert.GreaterOrEqual(t, instance.Count(), prevCount,
				"instance %d interval %d: count must not decrease (was %d, got %d)",
				instanceID, interval, prevCount, instance.Count())
			assert.GreaterOrEqual(t, instance.Sum(), prevSum,
				"instance %d interval %d: sum must not decrease (was %f, got %f)",
				instanceID, interval, prevSum, instance.Sum())

			prevCount = instance.Count()
			prevSum = instance.Sum()
		}
	}
}
