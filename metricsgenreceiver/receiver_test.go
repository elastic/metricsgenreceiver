package metricsgenreceiver

import (
	"context"
	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/dp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"testing"
	"time"
)

func TestReceiver(t *testing.T) {
	tests := []struct {
		name            string
		path            string
		dataPoints      int
		resourceMetrics int
		customizer      func(cfg *Config)
	}{
		{
			name:            "metricstemplate",
			path:            "testdata/metricstemplate",
			dataPoints:      3,
			resourceMetrics: 1,
		},
		{
			name:            "metricstemplate concurrency 1",
			path:            "testdata/metricstemplate",
			dataPoints:      3,
			resourceMetrics: 1,
			customizer:      func(cfg *Config) { cfg.Scenarios[0].Concurrency = 1 },
		},
		{
			name:            "metricstemplate concurrency 2",
			path:            "testdata/metricstemplate",
			dataPoints:      3,
			resourceMetrics: 1,
			customizer:      func(cfg *Config) { cfg.Scenarios[0].Concurrency = 2 },
		},
		{
			name:            "metricstemplate jitter",
			path:            "testdata/metricstemplate",
			dataPoints:      3,
			resourceMetrics: 1,
			customizer:      func(cfg *Config) { cfg.IntervalJitterStdDev = 5 * time.Millisecond },
		},
		{
			name:            "hostmetrics",
			path:            "builtin/hostmetrics",
			dataPoints:      90,
			resourceMetrics: 6,
		},
		{
			name:            "kubeletstats-node",
			path:            "builtin/kubeletstats-node",
			dataPoints:      17,
			resourceMetrics: 1,
		},
		{
			name:            "kubeletstats-pod",
			path:            "builtin/kubeletstats-pod",
			dataPoints:      34,
			resourceMetrics: 3,
		},
		{
			name:            "tsbs-devops",
			path:            "builtin/tsbs-devops",
			dataPoints:      101,
			resourceMetrics: 9,
		},
		{
			name:            "elasticapm-service-metrics",
			path:            "builtin/elasticapm-service-metrics",
			dataPoints:      4,
			resourceMetrics: 1,
		},
		{
			name:            "elasticapm-span-destination-metrics",
			path:            "builtin/elasticapm-span-destination-metrics",
			dataPoints:      20,
			resourceMetrics: 1,
			customizer: func(cfg *Config) {
				cfg.Scenarios[0].TemplateVars = map[string]any{
					"destinations": 10,
				}
			},
		},
		{
			name:            "elasticapm-transaction-metrics",
			path:            "builtin/elasticapm-transaction-metrics",
			dataPoints:      40,
			resourceMetrics: 1,
			customizer: func(cfg *Config) {
				cfg.Scenarios[0].TemplateVars = map[string]any{
					"services":     2,
					"transactions": 10,
				}
			},
		},
		{
			name:            "simple",
			path:            "builtin/simple",
			dataPoints:      3,
			resourceMetrics: 1,
			customizer: func(cfg *Config) {
				cfg.Scenarios[0].TemplateVars = map[string]any{
					"gauge_pct": 1,
					"gauge_int": 1,
					"counter":   1,
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sink := new(consumertest.MetricsSink)

			factory := NewFactory()
			cfg := testdataConfigYamlAsMap()
			cfg.Scenarios[0].Path = test.path
			if test.customizer != nil {
				test.customizer(cfg)
			}
			rcv, err := factory.CreateMetrics(context.Background(), receivertest.NewNopSettings(typ), cfg, sink)
			require.NoError(t, err)
			err = rcv.Start(context.Background(), nil)
			require.NoError(t, err)

			require.EventuallyWithT(t, func(c *assert.CollectT) {
				require.Equal(c, test.dataPoints*
					2* // 2 intervals
					cfg.Scenarios[0].Scale, sink.DataPointCount())
			}, 2*time.Second, time.Millisecond)
			require.NoError(t, rcv.Shutdown(context.Background()))

			allMetrics := sink.AllMetrics()
			require.NotEmpty(t, allMetrics)

			require.Equal(t, 2*cfg.Scenarios[0].Scale, len(allMetrics))

			if cfg.Scenarios[0].Concurrency <= 1 {
				verifyMetrics(t, 0, cfg, allMetrics, cfg.StartTime)
				verifyMetrics(t, cfg.Scenarios[0].Scale, cfg, allMetrics, cfg.StartTime.Add(cfg.Interval))
			}
		})
	}
}

func verifyMetrics(t *testing.T, offset int, cfg *Config, allMetrics []pmetric.Metrics, timestamp time.Time) {
	for i := offset; i < cfg.Scenarios[0].Scale+offset; i++ {
		dp.ForEachDataPoint(&allMetrics[i], func(r pcommon.Resource, s pcommon.InstrumentationScope, m pmetric.Metric, dp dp.DataPoint) {
			r.Attributes().Range(func(k string, v pcommon.Value) bool {
				require.NotEmpty(t, k)
				require.NotEmpty(t, v)
				return true
			})
			require.Equal(t, cfg.StartTime, dp.StartTimestamp().AsTime())
			require.WithinRange(t, dp.Timestamp().AsTime(), timestamp, timestamp.Add(cfg.IntervalJitterStdDev*5))
		})
	}
}
