package metricsgenreceiver

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/distribution"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

type nopConsumer struct {
	dps atomic.Uint64
}

func (c *nopConsumer) ConsumeMetrics(_ context.Context, md pmetric.Metrics) error {
	c.dps.Add(uint64(md.DataPointCount()))
	return nil
}

func (c *nopConsumer) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: false}
}

// benchBaseConfig returns a Config with a fixed 10-minute window at 10s intervals (60 ticks).
// Callers append exactly one ScenarioCfg before passing to runBench.
func benchBaseConfig() Config {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	return Config{
		StartTime:    start,
		EndTime:      start.Add(10 * time.Minute),
		Interval:     10 * time.Second,
		Seed:         42,
		Distribution: distribution.DefaultDistribution,
	}
}

func runBench(b *testing.B, cfg Config) {
	b.Helper()
	factory := NewFactory()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		nc := &nopConsumer{}
		cfgCopy := cfg
		rcv, err := factory.CreateMetrics(context.Background(), receivertest.NewNopSettings(typ), &cfgCopy, nc)
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()

		start := time.Now()
		if err := rcv.Start(context.Background(), nil); err != nil {
			b.Fatal(err)
		}
		waitForCompletion(rcv)
		elapsed := time.Since(start)

		b.StopTimer()
		if err := rcv.Shutdown(context.Background()); err != nil {
			b.Fatal(err)
		}

		got := nc.dps.Load()
		if got == 0 {
			b.Fatal("benchmark produced 0 data points — receiver may have exited early or config is invalid")
		}
		b.ReportMetric(float64(got)/elapsed.Seconds(), "dps/sec")
		b.Logf("data points: %d", got)
	}
}

func BenchmarkMetricsGenReceiver(b *testing.B) {
	// per-template sub-benchmarks at representative scales

	b.Run("hostmetrics", func(b *testing.B) {
		cfg := benchBaseConfig()
		cfg.Scenarios = []ScenarioCfg{{Path: "builtin/hostmetrics", Scale: 100}}
		runBench(b, cfg)
	})

	b.Run("kubeletstats-node", func(b *testing.B) {
		cfg := benchBaseConfig()
		cfg.Scenarios = []ScenarioCfg{{Path: "builtin/kubeletstats-node", Scale: 10}}
		runBench(b, cfg)
	})

	b.Run("kubeletstats-pod", func(b *testing.B) {
		cfg := benchBaseConfig()
		cfg.Scenarios = []ScenarioCfg{{Path: "builtin/kubeletstats-pod", Scale: 100}}
		runBench(b, cfg)
	})

	b.Run("elasticapm-service", func(b *testing.B) {
		cfg := benchBaseConfig()
		cfg.Scenarios = []ScenarioCfg{{Path: "builtin/elasticapm-service-metrics", Scale: 100}}
		runBench(b, cfg)
	})

	b.Run("elasticapm-span-dest", func(b *testing.B) {
		cfg := benchBaseConfig()
		cfg.Scenarios = []ScenarioCfg{{
			Path:         "builtin/elasticapm-span-destination-metrics",
			Scale:        100,
			TemplateVars: map[string]any{"destinations": 5},
		}}
		runBench(b, cfg)
	})

	b.Run("elasticapm-transaction", func(b *testing.B) {
		cfg := benchBaseConfig()
		cfg.Scenarios = []ScenarioCfg{{
			Path:         "builtin/elasticapm-transaction-metrics",
			Scale:        100,
			TemplateVars: map[string]any{"services": 10, "transactions": 10},
		}}
		runBench(b, cfg)
	})

	b.Run("nginx", func(b *testing.B) {
		cfg := benchBaseConfig()
		cfg.Scenarios = []ScenarioCfg{{Path: "builtin/nginx", Scale: 100}}
		runBench(b, cfg)
	})

	b.Run("node_exporter", func(b *testing.B) {
		cfg := benchBaseConfig()
		cfg.Scenarios = []ScenarioCfg{{Path: "builtin/node_exporter", Scale: 100}}
		runBench(b, cfg)
	})

	// cross-cutting variants using hostmetrics as the control

	b.Run("hostmetrics-concurrency", func(b *testing.B) {
		cfg := benchBaseConfig()
		cfg.Scenarios = []ScenarioCfg{{Path: "builtin/hostmetrics", Scale: 100, Concurrency: 4}}
		runBench(b, cfg)
	})

	b.Run("hostmetrics-churn", func(b *testing.B) {
		cfg := benchBaseConfig()
		cfg.Scenarios = []ScenarioCfg{{
			Path:  "builtin/hostmetrics",
			Scale: 100,
			Churn: &ChurnCfg{SamplesPerSeries: 10},
		}}
		runBench(b, cfg)
	})

	b.Run("hostmetrics-expohisto", func(b *testing.B) {
		cfg := benchBaseConfig()
		cfg.Scenarios = []ScenarioCfg{{
			Path:              "builtin/hostmetrics",
			Scale:             100,
			HistogramOverride: "exponential",
		}}
		runBench(b, cfg)
	})

	b.Run("hostmetrics-cumulative", func(b *testing.B) {
		cfg := benchBaseConfig()
		cfg.Scenarios = []ScenarioCfg{{
			Path:                "builtin/hostmetrics",
			Scale:               100,
			TemporalityOverride: "cumulative",
		}}
		runBench(b, cfg)
	})
}
