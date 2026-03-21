package metricsgenreceiver

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/dp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer"
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
			dataPoints:      170,
			resourceMetrics: 7,
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

func TestInstanceIDWithOffset(t *testing.T) {
	type testCase struct {
		name           string
		scale          int
		instanceOffset uint
		expectedIDs    []int
	}

	tests := []testCase{
		{
			name:           "scale=3, no-offset",
			scale:          3,
			instanceOffset: 0,
			expectedIDs:    []int{0, 1, 2},
		},
		{
			name:           "scale=3, offset-1000",
			scale:          3,
			instanceOffset: 1000,
			expectedIDs:    []int{1000, 1001, 1002},
		},
		{
			name:           "scale=2, large-offset",
			scale:          2,
			instanceOffset: 9999,
			expectedIDs:    []int{9999, 10000},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sink := new(consumertest.MetricsSink)
			factory := NewFactory()
			cfg := testdataConfigYamlAsMap()
			cfg.Scenarios[0].Path = "builtin/simple"
			cfg.Scenarios[0].Scale = tc.scale
			cfg.InstanceOffset = tc.instanceOffset
			cfg.Scenarios[0].TemplateVars = map[string]any{"gauge_pct": 1, "gauge_int": 0, "counter": 0}

			rcv, err := factory.CreateMetrics(context.Background(), receivertest.NewNopSettings(typ), cfg, sink)
			require.NoError(t, err)
			err = rcv.Start(context.Background(), nil)
			require.NoError(t, err)

			require.EventuallyWithT(t, func(c *assert.CollectT) {
				require.Greater(c, sink.DataPointCount(), 0)
			}, 2*time.Second, time.Millisecond)
			require.NoError(t, rcv.Shutdown(context.Background()))

			hosts := collectHostNames(sink.AllMetrics())
			require.Len(t, hosts, tc.scale)

			ids := make([]int, 0, len(hosts))

			for _, h := range hosts {
				parts := strings.Split(h, "-")
				require.Len(t, parts, 2, "host name should have two parts")

				hostID, errParse := strconv.Atoi(parts[1])
				require.NoError(t, errParse, "host ID should be an integer")

				ids = append(ids, hostID)
			}
			sort.Ints(ids)
			assert.Equal(t, tc.expectedIDs, ids, "instance IDs should match expected range reflecting the offset")
		})
	}
}

// TestRealtimeDefaultsToRunningIndefinitely verifies that a real_time receiver with no
// end_time or end_now_minus configured does not exit immediately.
func TestRealtimeDefaultsToRunningIndefinitely(t *testing.T) {
	interval := 100 * time.Millisecond
	sink := new(consumertest.MetricsSink)

	factory := NewFactory()
	cfg := &Config{
		Interval: interval,
		RealTime: true,
		Seed:     42,
		Scenarios: []ScenarioCfg{{Path: "testdata/metricstemplate", Scale: 1}},
	}

	rcv, err := factory.CreateMetrics(context.Background(), receivertest.NewNopSettings(typ), cfg, sink)
	require.NoError(t, err)
	err = rcv.Start(context.Background(), nil)
	require.NoError(t, err)

	// Receiver should keep producing metrics across multiple intervals.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		require.Greater(c, sink.DataPointCount(), 3)
	}, 2*time.Second, time.Millisecond)

	require.NoError(t, rcv.Shutdown(context.Background()))
}

// TestRealtimeTimestampsTrackWallClock verifies that metric timestamps stay in sync with
// wall clock time in realtime mode. Each batch of metrics should have a timestamp close
// to the wall clock time at which it was produced.
func TestRealtimeTimestampsTrackWallClock(t *testing.T) {
	const (
		interval  = 200 * time.Millisecond
		batches   = 5
		tolerance = interval
	)

	type observation struct {
		wallClock       time.Time
		metricTimestamp time.Time
	}
	var (
		mu   sync.Mutex
		obs  []observation
	)

	consumer := &timestampRecordingConsumer{
		onConsume: func(md pmetric.Metrics) {
			wall := time.Now()
			var metricTs time.Time
			dp.ForEachDataPoint(&md, func(_ pcommon.Resource, _ pcommon.InstrumentationScope, _ pmetric.Metric, d dp.DataPoint) {
				if metricTs.IsZero() {
					metricTs = d.Timestamp().AsTime()
				}
			})
			mu.Lock()
			obs = append(obs, observation{wallClock: wall, metricTimestamp: metricTs})
			mu.Unlock()
		},
	}

	factory := NewFactory()
	cfg := &Config{
		Interval: interval,
		RealTime: true,
		Seed:     42,
		Scenarios: []ScenarioCfg{{Path: "testdata/metricstemplate", Scale: 1}},
	}

	rcv, err := factory.CreateMetrics(context.Background(), receivertest.NewNopSettings(typ), cfg, consumer)
	require.NoError(t, err)
	require.NoError(t, rcv.Start(context.Background(), nil))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		mu.Lock()
		defer mu.Unlock()
		require.GreaterOrEqual(c, len(obs), batches)
	}, 5*time.Second, time.Millisecond)

	require.NoError(t, rcv.Shutdown(context.Background()))

	mu.Lock()
	defer mu.Unlock()
	for i, o := range obs[:batches] {
		diff := o.wallClock.Sub(o.metricTimestamp)
		if diff < 0 {
			diff = -diff
		}
		assert.LessOrEqualf(t, diff, tolerance,
			"batch %d: metric timestamp %v is %v away from wall clock %v",
			i, o.metricTimestamp, diff, o.wallClock)
	}
}

type timestampRecordingConsumer struct {
	onConsume func(pmetric.Metrics)
}

func (c *timestampRecordingConsumer) ConsumeMetrics(_ context.Context, md pmetric.Metrics) error {
	c.onConsume(md)
	return nil
}

func (c *timestampRecordingConsumer) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{}
}

// TestRealtimeShutdownCompletesPromptly verifies that Shutdown unblocks the goroutine
// immediately via context cancellation rather than waiting for the next ticker tick.
// Without the select on ctx.Done() the goroutine stays blocked on <-ticker.C for up
// to one full interval after Shutdown is called, causing the receiver to stall.
func TestRealtimeShutdownCompletesPromptly(t *testing.T) {
	interval := 500 * time.Millisecond
	sink := new(consumertest.MetricsSink)

	factory := NewFactory()
	now := time.Now()
	cfg := &Config{
		StartTime: now,
		EndTime:   now.Add(10 * time.Minute),
		Interval:  interval,
		RealTime:  true,
		Seed:      42,
		Scenarios: []ScenarioCfg{{Path: "testdata/metricstemplate", Scale: 1}},
	}

	rcv, err := factory.CreateMetrics(context.Background(), receivertest.NewNopSettings(typ), cfg, sink)
	require.NoError(t, err)
	err = rcv.Start(context.Background(), nil)
	require.NoError(t, err)

	// Wait until the first batch is produced; the goroutine is now blocked on ticker.C.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		require.Greater(c, sink.DataPointCount(), 0)
	}, interval*3, time.Millisecond)

	// Shutdown must complete well within one interval.
	// Without the ctx.Done() select branch, Shutdown blocks until the next tick fires
	// (up to `interval` away), causing a stall when multiple receivers are running.
	shutdownStart := time.Now()
	require.NoError(t, rcv.Shutdown(context.Background()))
	require.Less(t, time.Since(shutdownStart), interval/2,
		"Shutdown stalled - goroutine was not unblocked by context cancellation")
}

func collectHostNames(allMetrics []pmetric.Metrics) []string {
	hostSet := make(map[string]bool)

	for _, metrics := range allMetrics {
		for i := 0; i < metrics.ResourceMetrics().Len(); i++ {
			rm := metrics.ResourceMetrics().At(i)
			if hostVal, ok := rm.Resource().Attributes().Get("host.name"); ok {
				hostSet[hostVal.Str()] = true
			}
		}
	}

	hosts := make([]string, 0, len(hostSet))

	for host := range hostSet {
		hosts = append(hosts, host)
	}

	sort.Strings(hosts)

	return hosts
}
