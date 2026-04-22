package metricstmpl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func TestExtractGenerationHintsNoMetadataIsNoop(t *testing.T) {
	metrics := pmetric.NewMetrics()
	m := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("test.metric")
	m.SetEmptyGauge()

	hints, err := ExtractGenerationHints(&metrics)
	require.NoError(t, err)
	assert.Empty(t, hints)
}

func TestExtractGenerationHintsReadsAndStripsMetadata(t *testing.T) {
	metrics := pmetric.NewMetrics()
	m := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("test.metric")
	m.SetEmptyGauge()
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
	m := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("test.metric")
	m.SetEmptyGauge()
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
	m := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("test.metric")
	m.SetEmptyGauge()
	m.Metadata().PutStr(GenerationHintMetadataKey, "")

	hints, err := ExtractGenerationHints(&metrics)
	require.Error(t, err)
	assert.Nil(t, hints)
	assert.Contains(t, err.Error(), "missing a class")
	assert.Contains(t, err.Error(), "test.metric")
}

func TestExtractGenerationHintsNonStringMetadata(t *testing.T) {
	metrics := pmetric.NewMetrics()
	m := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	m.SetName("test.metric")
	m.SetEmptyGauge()
	m.Metadata().PutInt(GenerationHintMetadataKey, 42)

	hints, err := ExtractGenerationHints(&metrics)
	require.Error(t, err)
	assert.Nil(t, hints)
	assert.Contains(t, err.Error(), "non-string")
}
