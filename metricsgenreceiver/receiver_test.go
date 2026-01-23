package metricsgenreceiver

import (
	"context"
	"testing"
	"time"

	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/dp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver/receivertest"
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
			dataPoints:      139,
			resourceMetrics: 8,
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

func TestHistogramRandomization(t *testing.T) {
	tests := []struct {
		name                              string
		exponentialHistogramsTemplatePath string
		histogramOverride                 string
		expectedHistogramType             pmetric.MetricType
		expectedExponentialHistogramType  pmetric.MetricType
		verifyHistogramFunc               func(t *testing.T, m pmetric.Metric)
		verifyExponentialHistogramFunc    func(t *testing.T, m pmetric.Metric)
	}{
		{
			name:                              "default behavior - no override",
			exponentialHistogramsTemplatePath: "",
			histogramOverride:                 "",
			expectedHistogramType:             pmetric.MetricTypeHistogram,
			expectedExponentialHistogramType:  pmetric.MetricTypeExponentialHistogram,
			verifyHistogramFunc: func(t *testing.T, m pmetric.Metric) {
				// Verify histogram has randomized values
				dp := m.Histogram().DataPoints().At(0)
				assert.Greater(t, dp.Count(), uint64(0), "histogram should have count > 0")
				assert.Greater(t, dp.BucketCounts().Len(), 0, "histogram should have buckets")
			},
			verifyExponentialHistogramFunc: func(t *testing.T, m pmetric.Metric) {
				// Verify exponential histogram uses low-frequency template (default)
				dp := m.ExponentialHistogram().DataPoints().At(0)
				assert.Greater(t, dp.Count(), uint64(0), "exponential histogram should have count > 0")
				assert.Less(t, dp.Count(), uint64(30), "low-frequency template should generate count < 30")
				assert.NotEqual(t, int32(0), dp.Scale(), "exponential histogram should have a scale")
				assert.Equal(t, pmetric.AggregationTemporalityDelta, m.ExponentialHistogram().AggregationTemporality(), "should use delta temporality")
			},
		},
		{
			name:                              "force exponential histograms - low frequency",
			exponentialHistogramsTemplatePath: "",
			histogramOverride:                 "exponential",
			expectedHistogramType:             pmetric.MetricTypeExponentialHistogram,
			expectedExponentialHistogramType:  pmetric.MetricTypeExponentialHistogram,
			verifyHistogramFunc: func(t *testing.T, m pmetric.Metric) {
				// Verify that the original histogram was converted to exponential using low-frequency template
				dp := m.ExponentialHistogram().DataPoints().At(0)
				assert.Greater(t, dp.Count(), uint64(0), "converted histogram should have count > 0")
				assert.Less(t, dp.Count(), uint64(30), "low-frequency template should generate count < 30")
				assert.NotEqual(t, int32(0), dp.Scale(), "converted histogram should have a scale")
				assert.Equal(t, pmetric.AggregationTemporalityDelta, m.ExponentialHistogram().AggregationTemporality(), "should use delta temporality")
			},
			verifyExponentialHistogramFunc: func(t *testing.T, m pmetric.Metric) {
				// Verify exponential histogram has randomized values from low-frequency template
				dp := m.ExponentialHistogram().DataPoints().At(0)
				assert.Greater(t, dp.Count(), uint64(0), "exponential histogram should have count > 0")
				assert.Less(t, dp.Count(), uint64(30), "low-frequency template should generate count < 30")
				assert.Equal(t, pmetric.AggregationTemporalityDelta, m.ExponentialHistogram().AggregationTemporality(), "should use delta temporality")
			},
		},
		{
			name:                              "force exponential histograms - high frequency",
			exponentialHistogramsTemplatePath: "builtin/exponential-histograms-high-frequency.ndjson",
			histogramOverride:                 "exponential",
			expectedHistogramType:             pmetric.MetricTypeExponentialHistogram,
			expectedExponentialHistogramType:  pmetric.MetricTypeExponentialHistogram,
			verifyHistogramFunc: func(t *testing.T, m pmetric.Metric) {
				// Verify that the original histogram was converted to exponential using high-frequency template
				dp := m.ExponentialHistogram().DataPoints().At(0)
				assert.Greater(t, dp.Count(), uint64(200), "high-frequency template should generate count > 200")
				assert.NotEqual(t, int32(0), dp.Scale(), "converted histogram should have a scale")
				assert.Equal(t, pmetric.AggregationTemporalityDelta, m.ExponentialHistogram().AggregationTemporality(), "should use delta temporality")
			},
			verifyExponentialHistogramFunc: func(t *testing.T, m pmetric.Metric) {
				// Verify exponential histogram has randomized values from high-frequency template
				dp := m.ExponentialHistogram().DataPoints().At(0)
				assert.Greater(t, dp.Count(), uint64(200), "high-frequency template should generate count > 200")
				assert.Equal(t, pmetric.AggregationTemporalityDelta, m.ExponentialHistogram().AggregationTemporality(), "should use delta temporality")
			},
		},
		{
			name:                              "custom exponential histogram template without override",
			exponentialHistogramsTemplatePath: "builtin/exponential-histograms-high-frequency.ndjson",
			histogramOverride:                 "",
			expectedHistogramType:             pmetric.MetricTypeHistogram,
			expectedExponentialHistogramType:  pmetric.MetricTypeExponentialHistogram,
			verifyHistogramFunc: func(t *testing.T, m pmetric.Metric) {
				// Histogram should remain as histogram with distribution applied
				dp := m.Histogram().DataPoints().At(0)
				assert.Greater(t, dp.Count(), uint64(0), "histogram should have count > 0")
			},
			verifyExponentialHistogramFunc: func(t *testing.T, m pmetric.Metric) {
				// Exponential histogram should use the high-frequency template
				dp := m.ExponentialHistogram().DataPoints().At(0)
				assert.Greater(t, dp.Count(), uint64(200), "high-frequency template should generate count > 200")
				assert.Equal(t, pmetric.AggregationTemporalityDelta, m.ExponentialHistogram().AggregationTemporality(), "should use delta temporality")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sink := new(consumertest.MetricsSink)

			factory := NewFactory()
			startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			cfg := &Config{
				StartTime: startTime,
				EndTime:   startTime.Add(30 * time.Second),
				Interval:  30 * time.Second,
				Seed:      42,
				Scenarios: []ScenarioCfg{
					{
						Path:        "testdata/histogram-template",
						Scale:       10,
						Concurrency: 1,
					},
				},
			}
			if test.exponentialHistogramsTemplatePath != "" {
				cfg.ExponentialHistogramsTemplatePath = test.exponentialHistogramsTemplatePath
			}
			if test.histogramOverride != "" {
				cfg.Scenarios[0].HistogramOverride = test.histogramOverride
			}

			rcv, err := factory.CreateMetrics(context.Background(), receivertest.NewNopSettings(typ), cfg, sink)
			require.NoError(t, err)
			err = rcv.Start(context.Background(), nil)
			require.NoError(t, err)

			require.EventuallyWithT(t, func(c *assert.CollectT) {
				// 2 metrics (histogram + exponential histogram) * 1 interval * scale
				require.Equal(c, 2*cfg.Scenarios[0].Scale, sink.DataPointCount())
			}, 2*time.Second, time.Millisecond)
			require.NoError(t, rcv.Shutdown(context.Background()))

			allMetrics := sink.AllMetrics()
			require.NotEmpty(t, allMetrics)

			// Verify metrics from the first interval
			firstBatch := allMetrics[0]
			var histogramMetric, exponentialHistogramMetric pmetric.Metric
			dp.ForEachMetric(&firstBatch, func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric) {
				if m.Name() == "test.histogram" {
					histogramMetric = m
				} else if m.Name() == "test.exponential_histogram" {
					exponentialHistogramMetric = m
				}
			})

			require.NotNil(t, histogramMetric, "histogram metric should be present")
			require.NotNil(t, exponentialHistogramMetric, "exponential histogram metric should be present")

			// Verify the histogram metric type matches expectations
			assert.Equal(t, test.expectedHistogramType, histogramMetric.Type(), "histogram metric type should match expected")
			test.verifyHistogramFunc(t, histogramMetric)

			// Verify the exponential histogram metric type matches expectations
			assert.Equal(t, test.expectedExponentialHistogramType, exponentialHistogramMetric.Type(), "exponential histogram metric type should match expected")
			test.verifyExponentialHistogramFunc(t, exponentialHistogramMetric)
		})
	}
}

func TestHistogramRandomizationDiversity(t *testing.T) {
	// Test that histogram values are actually randomized across multiple intervals
	sink := new(consumertest.MetricsSink)

	factory := NewFactory()
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cfg := &Config{
		StartTime: startTime,
		EndTime:   startTime.Add(60 * time.Second),
		Interval:  30 * time.Second,
		Seed:      999,
		Scenarios: []ScenarioCfg{
			{
				Path:        "testdata/histogram-template",
				Scale:       1,
				Concurrency: 1,
			},
		},
	}

	rcv, err := factory.CreateMetrics(context.Background(), receivertest.NewNopSettings(typ), cfg, sink)
	require.NoError(t, err)
	err = rcv.Start(context.Background(), nil)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		require.Equal(c, 2*2*cfg.Scenarios[0].Scale, sink.DataPointCount())
	}, 2*time.Second, time.Millisecond)
	require.NoError(t, rcv.Shutdown(context.Background()))

	allMetrics := sink.AllMetrics()
	require.Len(t, allMetrics, 2)

	// Collect histogram counts from both intervals
	histogramCounts := make([]uint64, 0)
	exponentialHistogramCounts := make([]uint64, 0)

	for _, metrics := range allMetrics {
		dp.ForEachMetric(&metrics, func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric) {
			if m.Name() == "test.histogram" && m.Type() == pmetric.MetricTypeHistogram {
				dp := m.Histogram().DataPoints().At(0)
				histogramCounts = append(histogramCounts, dp.Count())
			} else if m.Name() == "test.exponential_histogram" && m.Type() == pmetric.MetricTypeExponentialHistogram {
				dp := m.ExponentialHistogram().DataPoints().At(0)
				exponentialHistogramCounts = append(exponentialHistogramCounts, dp.Count())
			}
		})
	}

	// Verify we have values from both intervals
	require.Len(t, histogramCounts, 2, "should have histogram counts from both intervals")
	require.Len(t, exponentialHistogramCounts, 2, "should have exponential histogram counts from both intervals")

	// Verify that at least one metric type has different values (indicating randomization is working)
	// Note: Due to randomness, they could theoretically be the same, but it's very unlikely
	histogramDifferent := histogramCounts[0] != histogramCounts[1]
	exponentialHistogramDifferent := exponentialHistogramCounts[0] != exponentialHistogramCounts[1]

	assert.True(t, histogramDifferent || exponentialHistogramDifferent,
		"at least one histogram type should have different counts between intervals, indicating randomization is working")
}
