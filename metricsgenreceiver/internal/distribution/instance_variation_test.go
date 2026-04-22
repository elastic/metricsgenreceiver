package distribution

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func applyVariation(v pmetric.NumberDataPoint, instanceID int, metric pmetric.Metric, hints map[string]MetricGenerationHint) {
	ApplyInstanceVariation(v, instanceID, IdentityHash(metric.Name(), v.Attributes()), metric, hints, nil)
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
