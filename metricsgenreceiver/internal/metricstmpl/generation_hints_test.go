package metricstmpl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func TestExtractGenerationHintsNoMetadataIsNoop(t *testing.T) {
	metrics := pmetric.NewMetrics()
	appendGaugeMetric(metrics, "test.metric")

	hints, err := ExtractGenerationHints(&metrics)
	require.NoError(t, err)
	assert.Empty(t, hints)
}

func TestExtractGenerationHintsReadsAndStripsMetadata(t *testing.T) {
	metrics := pmetric.NewMetrics()
	m := appendGaugeMetric(metrics, "test.metric")
	m.Metadata().PutStr(GenerationHintMetadataKey, "slow_gauge")
	m.Metadata().PutStr("prometheus.type", "gauge")

	hints, err := ExtractGenerationHints(&metrics)
	require.NoError(t, err)
	require.Len(t, hints, 1)
	assert.Equal(t, "slow_gauge", string(hints["test.metric"].Class))

	// Hint metadata is stripped; unrelated metadata is left alone.
	_, hasHint := m.Metadata().Get(GenerationHintMetadataKey)
	assert.False(t, hasHint)
	_, hasPromType := m.Metadata().Get("prometheus.type")
	assert.True(t, hasPromType)
}

func TestExtractGenerationHintsInvalidClass(t *testing.T) {
	metrics := pmetric.NewMetrics()
	m := appendGaugeMetric(metrics, "test.metric")
	m.Metadata().PutStr(GenerationHintMetadataKey, "not_a_real_hint")

	hints, err := ExtractGenerationHints(&metrics)
	require.Error(t, err)
	assert.Nil(t, hints)
	assert.Contains(t, err.Error(), "unsupported class")
	assert.Contains(t, err.Error(), "test.metric")

	// Metadata is not stripped on error so the failure stays inspectable.
	v, ok := m.Metadata().Get(GenerationHintMetadataKey)
	require.True(t, ok)
	assert.Equal(t, "not_a_real_hint", v.Str())
}

func TestExtractGenerationHintsMissingClass(t *testing.T) {
	metrics := pmetric.NewMetrics()
	m := appendGaugeMetric(metrics, "test.metric")
	m.Metadata().PutStr(GenerationHintMetadataKey, "")

	hints, err := ExtractGenerationHints(&metrics)
	require.Error(t, err)
	assert.Nil(t, hints)
	assert.Contains(t, err.Error(), "missing a class")
	assert.Contains(t, err.Error(), "test.metric")
}

func TestExtractGenerationHintsNonStringMetadata(t *testing.T) {
	metrics := pmetric.NewMetrics()
	m := appendGaugeMetric(metrics, "test.metric")
	m.Metadata().PutInt(GenerationHintMetadataKey, 42)

	hints, err := ExtractGenerationHints(&metrics)
	require.Error(t, err)
	assert.Nil(t, hints)
	assert.Contains(t, err.Error(), "non-string")
}

func TestExtractGenerationHintsRepeatedMetricNameSameHintIsAllowed(t *testing.T) {
	metrics := pmetric.NewMetrics()
	first := appendGaugeMetric(metrics, "test.metric")
	second := appendGaugeMetric(metrics, "test.metric")
	first.Metadata().PutStr(GenerationHintMetadataKey, "steady_counter")
	second.Metadata().PutStr(GenerationHintMetadataKey, "steady_counter")

	hints, err := ExtractGenerationHints(&metrics)
	require.NoError(t, err)
	require.Len(t, hints, 1)
	assert.Equal(t, "steady_counter", string(hints["test.metric"].Class))

	_, firstHasHint := first.Metadata().Get(GenerationHintMetadataKey)
	_, secondHasHint := second.Metadata().Get(GenerationHintMetadataKey)
	assert.False(t, firstHasHint)
	assert.False(t, secondHasHint)
}

func TestExtractGenerationHintsRepeatedMetricNameDifferentHintFails(t *testing.T) {
	metrics := pmetric.NewMetrics()
	first := appendGaugeMetric(metrics, "test.metric")
	second := appendGaugeMetric(metrics, "test.metric")
	first.Metadata().PutStr(GenerationHintMetadataKey, "steady_counter")
	second.Metadata().PutStr(GenerationHintMetadataKey, "slow_gauge")

	hints, err := ExtractGenerationHints(&metrics)
	require.Error(t, err)
	assert.Nil(t, hints)
	assert.Contains(t, err.Error(), "same metricsgen.hint.class across all occurrences")

	v, ok := first.Metadata().Get(GenerationHintMetadataKey)
	require.True(t, ok)
	assert.Equal(t, "steady_counter", v.Str())
}

func TestExtractGenerationHintsRepeatedMetricNameMixedHintPresenceFails(t *testing.T) {
	metrics := pmetric.NewMetrics()
	first := appendGaugeMetric(metrics, "test.metric")
	appendGaugeMetric(metrics, "test.metric")
	first.Metadata().PutStr(GenerationHintMetadataKey, "steady_counter")

	hints, err := ExtractGenerationHints(&metrics)
	require.Error(t, err)
	assert.Nil(t, hints)
	assert.Contains(t, err.Error(), "declare metricsgen.hint.class consistently across all occurrences")
}

func TestExtractGenerationHintsRejectsNonNumberMetrics(t *testing.T) {
	tests := []struct {
		name   string
		append func(pmetric.Metrics, string) pmetric.Metric
	}{
		{
			name:   "histogram",
			append: appendHistogramMetric,
		},
		{
			name:   "exponential histogram",
			append: appendExponentialHistogramMetric,
		},
		{
			name:   "summary",
			append: appendSummaryMetric,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := pmetric.NewMetrics()
			m := tt.append(metrics, "test.metric")
			m.Metadata().PutStr(GenerationHintMetadataKey, "constant")

			hints, err := ExtractGenerationHints(&metrics)
			require.Error(t, err)
			assert.Nil(t, hints)
			assert.Contains(t, err.Error(), "must be a gauge or sum")
		})
	}
}

func appendGaugeMetric(metrics pmetric.Metrics, name string) pmetric.Metric {
	m := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName(name)
	m.SetEmptyGauge()
	return m
}

func appendHistogramMetric(metrics pmetric.Metrics, name string) pmetric.Metric {
	m := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName(name)
	m.SetEmptyHistogram()
	return m
}

func appendExponentialHistogramMetric(metrics pmetric.Metrics, name string) pmetric.Metric {
	m := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName(name)
	m.SetEmptyExponentialHistogram()
	return m
}

func appendSummaryMetric(metrics pmetric.Metrics, name string) pmetric.Metric {
	m := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName(name)
	m.SetEmptySummary()
	return m
}
