package dp

import (
	"reflect"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func buildBenchMetrics() pmetric.Metrics {
	metrics := pmetric.NewMetrics()
	sm := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()
	add := func(n int, setup func(pmetric.Metric)) {
		for i := 0; i < n; i++ {
			setup(sm.Metrics().AppendEmpty())
		}
	}
	add(20, func(m pmetric.Metric) { m.SetEmptyGauge().DataPoints().AppendEmpty() })
	add(20, func(m pmetric.Metric) { m.SetEmptySum().DataPoints().AppendEmpty() })
	add(20, func(m pmetric.Metric) { m.SetEmptyHistogram().DataPoints().AppendEmpty() })
	add(20, func(m pmetric.Metric) { m.SetEmptyExponentialHistogram().DataPoints().AppendEmpty() })
	add(20, func(m pmetric.Metric) { m.SetEmptySummary().DataPoints().AppendEmpty() })
	return metrics // 100 data points across all metric types
}

// BenchmarkForEachDataPoint pins the per-call cost of the iteration helper.
// Expected: 1 alloc/data point — interface boxing of the concrete pdata type into
// the DataPoint interface argument. Any increase above this baseline is a regression
// in the iteration code itself. Decreasing it would require removing the interface.
func BenchmarkForEachDataPoint(b *testing.B) {
	metrics := buildBenchMetrics()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ForEachDataPoint(&metrics, func(int, pcommon.Resource, pcommon.InstrumentationScope, pmetric.Metric, DataPoint) {})
	}
}

func TestForEachDataPointIndexes(t *testing.T) {
	metrics := pmetric.NewMetrics()
	scopeMetrics := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty()

	gauge := scopeMetrics.Metrics().AppendEmpty()
	gauge.SetName("gauge")
	gauge.SetEmptyGauge().DataPoints().AppendEmpty()
	gauge.Gauge().DataPoints().AppendEmpty()

	sum := scopeMetrics.Metrics().AppendEmpty()
	sum.SetName("sum")
	sum.SetEmptySum().DataPoints().AppendEmpty()

	histogram := scopeMetrics.Metrics().AppendEmpty()
	histogram.SetName("histogram")
	histogram.SetEmptyHistogram().DataPoints().AppendEmpty()

	exponentialHistogram := scopeMetrics.Metrics().AppendEmpty()
	exponentialHistogram.SetName("exponential_histogram")
	exponentialHistogram.SetEmptyExponentialHistogram().DataPoints().AppendEmpty()

	summary := scopeMetrics.Metrics().AppendEmpty()
	summary.SetName("summary")
	summary.SetEmptySummary().DataPoints().AppendEmpty()

	var indexes []int
	var metricNames []string
	ForEachDataPoint(&metrics, func(index int, _ pcommon.Resource, _ pcommon.InstrumentationScope, metric pmetric.Metric, _ DataPoint) {
		indexes = append(indexes, index)
		metricNames = append(metricNames, metric.Name())
	})

	expectedIndexes := []int{0, 1, 2, 3, 4, 5}
	if !reflect.DeepEqual(expectedIndexes, indexes) {
		t.Fatalf("expected indexes %v, got %v", expectedIndexes, indexes)
	}

	expectedMetricNames := []string{"gauge", "gauge", "sum", "histogram", "exponential_histogram", "summary"}
	if !reflect.DeepEqual(expectedMetricNames, metricNames) {
		t.Fatalf("expected metric names %v, got %v", expectedMetricNames, metricNames)
	}
}
