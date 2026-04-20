package metricstmpl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

type builtInScenarioTestCase struct {
	name         string
	path         string
	templateVars map[string]any
}

func builtInScenarioTestCases() []builtInScenarioTestCase {
	return []builtInScenarioTestCase{
		{name: "hostmetrics", path: "builtin/hostmetrics"},
		{name: "kubeletstats-node", path: "builtin/kubeletstats-node"},
		{name: "kubeletstats-pod", path: "builtin/kubeletstats-pod"},
		{name: "tsbs-devops", path: "builtin/tsbs-devops"},
		{name: "elasticapm-service-metrics", path: "builtin/elasticapm-service-metrics"},
		{
			name: "elasticapm-span-destination-metrics",
			path: "builtin/elasticapm-span-destination-metrics",
			templateVars: map[string]any{
				"destinations": 10,
			},
		},
		{
			name: "elasticapm-transaction-metrics",
			path: "builtin/elasticapm-transaction-metrics",
			templateVars: map[string]any{
				"services":     2,
				"transactions": 10,
			},
		},
		{
			name: "simple",
			path: "builtin/simple",
			templateVars: map[string]any{
				"gauge_pct": 1,
				"gauge_int": 1,
				"counter":   1,
			},
		},
		{name: "nginx", path: "builtin/nginx"},
	}
}

func TestBuiltInScenariosUseConsistentNumberEncodingPerMetricFamily(t *testing.T) {
	for _, tc := range builtInScenarioTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			metrics, err := RenderMetricsTemplate(tc.path, tc.templateVars)
			require.NoError(t, err)

			encodings := map[string]map[pmetric.NumberDataPointValueType]struct{}{}
			for i := 0; i < metrics.ResourceMetrics().Len(); i++ {
				rm := metrics.ResourceMetrics().At(i)
				for j := 0; j < rm.ScopeMetrics().Len(); j++ {
					sm := rm.ScopeMetrics().At(j)
					for k := 0; k < sm.Metrics().Len(); k++ {
						m := sm.Metrics().At(k)
						switch m.Type() {
						case pmetric.MetricTypeGauge:
							recordNumberEncodings(encodings, m.Name(), m.Gauge().DataPoints())
						case pmetric.MetricTypeSum:
							recordNumberEncodings(encodings, m.Name(), m.Sum().DataPoints())
						}
					}
				}
			}

			for metricName, types := range encodings {
				assert.Lenf(t, types, 1, "metric family %q mixes asInt/asDouble encoding", metricName)
			}
		})
	}
}

func TestBuiltInScenariosAvoidAllZeroDoubleMetricFamilies(t *testing.T) {
	for _, tc := range builtInScenarioTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			metrics, err := RenderMetricsTemplate(tc.path, tc.templateVars)
			require.NoError(t, err)

			allZeroDouble := map[string]bool{}
			seenDouble := map[string]bool{}
			seenInt := map[string]bool{}
			seenNonZeroDouble := map[string]bool{}

			for i := 0; i < metrics.ResourceMetrics().Len(); i++ {
				rm := metrics.ResourceMetrics().At(i)
				for j := 0; j < rm.ScopeMetrics().Len(); j++ {
					sm := rm.ScopeMetrics().At(j)
					for k := 0; k < sm.Metrics().Len(); k++ {
						m := sm.Metrics().At(k)
						switch m.Type() {
						case pmetric.MetricTypeGauge:
							inspectDoubleSeedQuality(allZeroDouble, seenDouble, seenInt, seenNonZeroDouble, m.Name(), m.Gauge().DataPoints())
						case pmetric.MetricTypeSum:
							inspectDoubleSeedQuality(allZeroDouble, seenDouble, seenInt, seenNonZeroDouble, m.Name(), m.Sum().DataPoints())
						}
					}
				}
			}

			for metricName := range seenDouble {
				if seenInt[metricName] {
					continue
				}
				if seenNonZeroDouble[metricName] {
					continue
				}
				assert.Falsef(t, allZeroDouble[metricName], "metric family %q is double-valued but all initial values are zero", metricName)
			}
		})
	}
}

func recordNumberEncodings(encodings map[string]map[pmetric.NumberDataPointValueType]struct{}, name string, dps pmetric.NumberDataPointSlice) {
	if _, ok := encodings[name]; !ok {
		encodings[name] = map[pmetric.NumberDataPointValueType]struct{}{}
	}
	for i := 0; i < dps.Len(); i++ {
		encodings[name][dps.At(i).ValueType()] = struct{}{}
	}
}

func inspectDoubleSeedQuality(
	allZeroDouble map[string]bool,
	seenDouble map[string]bool,
	seenInt map[string]bool,
	seenNonZeroDouble map[string]bool,
	name string,
	dps pmetric.NumberDataPointSlice,
) {
	for i := 0; i < dps.Len(); i++ {
		dp := dps.At(i)
		switch dp.ValueType() {
		case pmetric.NumberDataPointValueTypeDouble:
			seenDouble[name] = true
			if _, ok := allZeroDouble[name]; !ok {
				allZeroDouble[name] = true
			}
			if dp.DoubleValue() != 0 {
				allZeroDouble[name] = false
				seenNonZeroDouble[name] = true
			}
		case pmetric.NumberDataPointValueTypeInt:
			seenInt[name] = true
		}
	}
}
