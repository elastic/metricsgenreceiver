package dp

import (
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func ForEachMetric(ms *pmetric.Metrics, visitor func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric)) {
	rms := ms.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		rm := rms.At(i)
		ilms := rm.ScopeMetrics()
		for j := 0; j < ilms.Len(); j++ {
			sm := ilms.At(j)
			metricsList := sm.Metrics()
			for k := 0; k < metricsList.Len(); k++ {
				m := metricsList.At(k)
				visitor(rm.Resource(), sm.Scope(), m)
			}
		}
	}
}

func ForEachDataPoint(ms *pmetric.Metrics, visitor func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric, dp DataPoint)) {
	ForEachMetric(ms, func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric) {
		//exhaustive:enforce
		switch m.Type() {
		case pmetric.MetricTypeGauge:
			ds := m.Gauge().DataPoints()
			for l := 0; l < ds.Len(); l++ {
				visitor(res, is, m, ds.At(l))
			}
		case pmetric.MetricTypeSum:
			ds := m.Sum().DataPoints()
			for l := 0; l < ds.Len(); l++ {
				visitor(res, is, m, ds.At(l))
			}
		case pmetric.MetricTypeHistogram:
			ds := m.Histogram().DataPoints()
			for l := 0; l < ds.Len(); l++ {
				visitor(res, is, m, ds.At(l))
			}
		case pmetric.MetricTypeExponentialHistogram:
			ds := m.ExponentialHistogram().DataPoints()
			for l := 0; l < ds.Len(); l++ {
				visitor(res, is, m, ds.At(l))
			}
		case pmetric.MetricTypeSummary:
			ds := m.Summary().DataPoints()
			for l := 0; l < ds.Len(); l++ {
				visitor(res, is, m, ds.At(l))
			}
		case pmetric.MetricTypeEmpty:
		}
	})
}

type DataPoint interface {
	Attributes() pcommon.Map
	StartTimestamp() pcommon.Timestamp
	SetStartTimestamp(pcommon.Timestamp)
	Timestamp() pcommon.Timestamp
	SetTimestamp(pcommon.Timestamp)
}
