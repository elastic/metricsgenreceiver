package dp

import (
	"reflect"
	"testing"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

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
