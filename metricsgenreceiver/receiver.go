package metricsgenreceiver

import (
	"context"
	"errors"
	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/distribution"
	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/dp"
	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/metadata"
	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/metricstmpl"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componentstatus"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/receiverhelper"
	"go.uber.org/zap"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

type MetricsGenReceiver struct {
	cfg       *Config
	obsreport *receiverhelper.ObsReport
	settings  receiver.Settings

	nextMetrics consumer.Metrics
	cancel      context.CancelFunc
	scenarios   []Scenario
}

type Scenario struct {
	config                     ScenarioCfg
	metricsTemplate            *pmetric.Metrics
	resourceAttributesTemplate pcommon.Resource
	resources                  []pcommon.Resource
}

func newMetricsGenReceiver(cfg *Config, set receiver.Settings) (*MetricsGenReceiver, error) {
	obsreport, err := receiverhelper.NewObsReport(receiverhelper.ObsReportSettings{
		ReceiverID:             set.ID,
		ReceiverCreateSettings: set,
	})
	if err != nil {
		return nil, err
	}

	nowish := time.Now().Truncate(time.Second)
	if cfg.StartTime.IsZero() {
		cfg.StartTime = nowish.Add(-cfg.StartNowMinus)
	}
	if cfg.EndTime.IsZero() {
		cfg.EndTime = nowish.Add(-cfg.EndNowMinus)
	}

	ra := rand.New(rand.NewSource(cfg.Seed))

	scenarios := make([]Scenario, 0, len(cfg.Scenarios))
	for _, scn := range cfg.Scenarios {

		metrics, err := metricstmpl.RenderMetricsTemplate(scn.Path, scn.TemplateVars)
		if err != nil {
			return nil, err
		}
		dp.ForEachDataPoint(&metrics, func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric, dp dp.DataPoint) {
			dp.SetStartTimestamp(pcommon.NewTimestampFromTime(cfg.StartTime))
			if scn.AggregationTemporalityOverride() != 0 {
				switch m.Type() {
				case pmetric.MetricTypeSum:
					m.Sum().SetAggregationTemporality(scn.AggregationTemporalityOverride())
				case pmetric.MetricTypeHistogram:
					m.Histogram().SetAggregationTemporality(scn.AggregationTemporalityOverride())
				case pmetric.MetricTypeExponentialHistogram:
					m.ExponentialHistogram().SetAggregationTemporality(scn.AggregationTemporalityOverride())
				default:
					// no-op
				}
			}
		})
		resources, err := metricstmpl.GetResources(scn.Path, cfg.StartTime, scn.Scale, scn.TemplateVars, ra)
		if err != nil {
			return nil, err
		}
		scenarios = append(scenarios, Scenario{
			config:          scn,
			metricsTemplate: &metrics,
			resources:       resources,
		})
	}

	return &MetricsGenReceiver{
		cfg:       cfg,
		settings:  set,
		obsreport: obsreport,
		scenarios: scenarios,
	}, nil
}

func (r *MetricsGenReceiver) Start(ctx context.Context, host component.Host) error {
	ctx = context.Background()
	ctx, r.cancel = context.WithCancel(ctx)
	go func() {
		start := time.Now()
		nextLog := start.Add(10 * time.Second)
		ticker := time.NewTicker(r.cfg.Interval)
		defer ticker.Stop()
		dataPoints := uint64(0)
		currentTime := r.cfg.StartTime
		for i := 0; currentTime.UnixNano() < r.cfg.EndTime.UnixNano(); i++ {
			if ctx.Err() != nil {
				return
			}
			if time.Now().After(nextLog) {
				progressPct := currentTime.Sub(r.cfg.StartTime).Seconds() / r.cfg.EndTime.Sub(r.cfg.StartTime).Seconds()
				duration := time.Now().Sub(start)
				r.settings.Logger.Info("generating metrics progress",
					zap.Int("progress_percent", int(progressPct*100)),
					zap.String("eta", (time.Duration(float64(duration.Nanoseconds())/progressPct)-duration).Round(time.Second).String()),
					zap.Uint64("datapoints", dataPoints),
					zap.Float64("data_points_per_second", float64(dataPoints)/time.Now().Sub(start).Seconds()))
				nextLog = nextLog.Add(10 * time.Second)
			}
			simulatedTime := addJitter(currentTime, r.cfg.IntervalJitterStdDev, r.cfg.Interval)
			dataPoints += r.produceMetrics(ctx, simulatedTime)
			r.applyChurn(i, simulatedTime)

			if r.cfg.RealTime {
				<-ticker.C
			}
			currentTime = currentTime.Add(r.cfg.Interval)
		}
		duration := time.Now().Sub(start)

		r.settings.Logger.Info("finished generating metrics",
			zap.Uint64("datapoints", dataPoints),
			zap.String("duration", duration.Round(time.Millisecond).String()),
			zap.Float64("data_points_per_second", float64(dataPoints)/duration.Seconds()))
		if r.cfg.ExitAfterEnd {
			componentstatus.ReportStatus(host, componentstatus.NewFatalErrorEvent(errors.New("exiting because exit_after_end is set to true")))
		}
	}()

	return nil
}

func addJitter(t time.Time, stdDev time.Duration, interval time.Duration) time.Time {
	if stdDev == 0 {
		return t
	}
	jitter := time.Duration(int64(math.Abs(rand.NormFloat64() * float64(stdDev))))
	if jitter >= interval {
		jitter = interval - 1
	}
	return t.Add(jitter)
}

func (r *MetricsGenReceiver) applyChurn(interval int, simulatedTime time.Time) {
	for _, scn := range r.scenarios {
		if scn.config.Churn == 0 {
			continue
		}

		ra := r.getRand()

		startTime := simulatedTime.Format(time.RFC3339)
		for i := 0; i < scn.config.Churn; i++ {
			id := scn.config.Scale + interval*scn.config.Churn + i
			resource, err := metricstmpl.RenderResource(scn.config.Path, id, startTime, scn.config.TemplateVars, ra)
			if err != nil {
				r.settings.Logger.Error("failed to apply churn", zap.Error(err))
			} else {
				scn.resources[id%len(scn.resources)] = resource
			}
		}
	}
}

func (r *MetricsGenReceiver) produceMetrics(ctx context.Context, currentTime time.Time) uint64 {
	dataPoints := new(uint64)
	wg := sync.WaitGroup{}
	for _, scn := range r.scenarios {
		ra := r.getRand()

		// we don't keep track of the data points for each instance individually to reduce memory pressure
		// we still advance the metrics template have a new baseline that's used when simulating the metrics for each individual instance
		// this makes sure counters are increasing over time
		dp.ForEachDataPoint(scn.metricsTemplate, func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric, dp dp.DataPoint) {
			distribution.AdvanceDataPoint(dp, ra, m, r.cfg.Distribution)
		})
		if scn.config.Concurrency == 0 {
			for i := range scn.config.Scale {
				*dataPoints += uint64(r.produceMetricsForInstance(ctx, currentTime, scn, scn.resources[i]))
			}
			continue
		}

		for i := 0; i < scn.config.Concurrency; i++ {
			wg.Add(1)
			go func() {

				defer wg.Done()
				for j := 0; j < scn.config.Scale/scn.config.Concurrency; j++ {
					resource := scn.resources[j+i*scn.config.Scale/scn.config.Concurrency]
					currentDataPoints := r.produceMetricsForInstance(ctx, currentTime, scn, resource)
					atomic.AddUint64(dataPoints, uint64(currentDataPoints))
				}
			}()
		}
	}
	wg.Wait()
	return *dataPoints
}

func (r *MetricsGenReceiver) produceMetricsForInstance(ctx context.Context, currentTime time.Time, scn Scenario, instanceResource pcommon.Resource) int {
	r.obsreport.StartMetricsOp(ctx)
	metrics := pmetric.NewMetrics()
	scn.metricsTemplate.CopyTo(metrics)
	resourceMetrics := metrics.ResourceMetrics()
	for j := 0; j < resourceMetrics.Len(); j++ {
		overrideExistingAttributes(instanceResource, resourceMetrics.At(j).Resource())
	}

	ra := r.getRand()

	dp.ForEachDataPoint(&metrics, func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric, dp dp.DataPoint) {
		distribution.AdvanceDataPoint(dp, ra, m, r.cfg.Distribution)
		dp.SetTimestamp(pcommon.NewTimestampFromTime(currentTime))
	})
	dataPoints := metrics.DataPointCount()
	err := r.nextMetrics.ConsumeMetrics(ctx, metrics)
	r.obsreport.EndMetricsOp(ctx, metadata.Type.String(), dataPoints, err)
	return dataPoints
}

func overrideExistingAttributes(source, target pcommon.Resource) {
	targetAttr := target.Attributes()
	source.Attributes().Range(func(k string, v pcommon.Value) bool {
		if _, exists := targetAttr.Get(k); exists {
			targetValue := targetAttr.PutEmpty(k)
			v.CopyTo(targetValue)
		}
		return true
	})
}

func (r *MetricsGenReceiver) Shutdown(_ context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	return nil
}

// getRand returns a new random number generator seeded with the configured seed.
// This is NOT thread-safe, so it should only be used in a single goroutine.
func (r *MetricsGenReceiver) getRand() *rand.Rand {
	return rand.New(rand.NewSource(r.cfg.Seed))
}
