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

var allowedAllZeroDoubleMetricFamilies = map[string]string{
	// Pressure stall time can legitimately remain zero on an otherwise healthy, lightly loaded host.
	"node_pressure_memory_stalled_seconds_total": "healthy hosts may report no stalled memory pressure time",
	// Pressure wait time can legitimately remain zero on an otherwise healthy, lightly loaded host.
	"node_pressure_memory_waiting_seconds_total": "healthy hosts may report no waiting memory pressure time",
	// Guest CPU time often remains at zero on a plain VM that is not itself running guest workloads.
	"node_cpu_guest_seconds_total": "plain VMs can legitimately report zero guest CPU time across all cores",
	// A synchronized clock can legitimately report no current estimated error at the capture instant.
	"node_timex_estimated_error_seconds": "a synchronized clock may report zero current estimated error",
	// A UTC-configured host can legitimately have a zero timezone offset.
	"node_time_zone_offset_seconds": "UTC hosts legitimately report a zero timezone offset",
	// A synchronized clock can legitimately have no observable offset at the capture instant.
	"node_timex_offset_seconds": "a synchronized clock may report zero current offset",
	// PPS-related metrics stay at zero when no PPS source is configured.
	"node_timex_pps_frequency_hertz": "hosts without a PPS source legitimately keep this at zero",
	"node_timex_pps_jitter_seconds":  "hosts without a PPS source legitimately keep this at zero",
	"node_timex_pps_shift_seconds":   "hosts without a PPS source legitimately keep this at zero",
	"node_timex_pps_stability_hertz": "hosts without a PPS source legitimately keep this at zero",
	// Many hosts run with no TAI offset configured.
	"node_timex_tai_offset_seconds": "hosts without TAI offset configuration legitimately report zero",
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
		{name: "node_exporter", path: "builtin/node_exporter"},
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
				if _, ok := allowedAllZeroDoubleMetricFamilies[metricName]; ok {
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
