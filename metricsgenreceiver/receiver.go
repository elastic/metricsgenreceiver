package metricsgenreceiver

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/distribution"
	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/dp"
	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/expohistogen"
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
)

type MetricsGenReceiver struct {
	cfg       *Config
	obsreport *receiverhelper.ObsReport
	settings  receiver.Settings

	baseRand    *rand.Rand // base random number generator seeded with the configured seed
	expHistoGen *expohistogen.Generator
	nextMetrics consumer.Metrics
	cancel      context.CancelFunc
	scenarios   []Scenario
	progress    *MetricsProgress
}

type Scenario struct {
	config                     ScenarioCfg
	metricsTemplate            *pmetric.Metrics
	resourceAttributesTemplate pcommon.Resource
	resources                  []pcommon.Resource
	precision                  map[string]int
}

type MetricsProgress struct {
	start      time.Time
	datapoints atomic.Uint64
}

func newMetricsProgress() *MetricsProgress {
	return &MetricsProgress{
		start:      time.Now(),
		datapoints: atomic.Uint64{},
	}
}

func (p *MetricsProgress) duration() time.Duration {
	return time.Since(p.start)
}
func (p *MetricsProgress) dataPointsPerSecond() float64 {
	return float64(p.datapoints.Load()) / p.duration().Seconds()
}

func (p *MetricsProgress) eta(progressPct float64) time.Duration {
	if progressPct == 0 {
		return time.Duration(0)
	}
	return time.Duration(float64(p.duration().Nanoseconds())/progressPct) - p.duration()
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

	baseRand := rand.New(rand.NewSource(cfg.Seed))
	expHistoGen, err := expohistogen.NewGenerator(cfg.GetExponentialHistogramsTemplatePath())
	if err != nil {
		return nil, err
	}

	scenarios := make([]Scenario, 0, len(cfg.Scenarios))
	for _, scn := range cfg.Scenarios {

		metrics, err := metricstmpl.RenderMetricsTemplate(scn.Path, scn.TemplateVars)
		if err != nil {
			return nil, err
		}

		if scn.ForceExponentialHistograms() {
			dp.ForEachMetric(&metrics, func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric) {
				replaceHistogramsWithExponentialHistograms(m)
			})
		}
		dp.ForEachDataPoint(&metrics, func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric, dp dp.DataPoint) {
			dp.SetStartTimestamp(pcommon.NewTimestampFromTime(cfg.StartTime))
			if scn.AggregationTemporalityOverride() != pmetric.AggregationTemporalityUnspecified {
				switch m.Type() {
				case pmetric.MetricTypeSum:
					m.Sum().SetAggregationTemporality(scn.AggregationTemporalityOverride())
				case pmetric.MetricTypeHistogram:
					m.Histogram().SetAggregationTemporality(scn.AggregationTemporalityOverride())
				default:
					// no-op
				}
			}
			// initialize exponential histograms with clean values and set their temporality to delta as we currently only support that
			if m.Type() == pmetric.MetricTypeExponentialHistogram {
				expHistoGen.GenerateInto(baseRand, dp.(pmetric.ExponentialHistogramDataPoint))
				m.ExponentialHistogram().SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
			}

		})
		resources, err := metricstmpl.GetResources(scn.Path, cfg.StartTime, scn.Scale, scn.TemplateVars, baseRand)
		if err != nil {
			return nil, err
		}
		scenarios = append(scenarios, Scenario{
			config:          scn,
			metricsTemplate: &metrics,
			resources:       resources,
			precision:       distribution.InferPrecision(&metrics),
		})
	}

	return &MetricsGenReceiver{
		cfg:         cfg,
		settings:    set,
		baseRand:    baseRand,
		expHistoGen: expHistoGen,
		obsreport:   obsreport,
		scenarios:   scenarios,
		progress:    newMetricsProgress(),
	}, nil
}

func (r *MetricsGenReceiver) Start(ctx context.Context, host component.Host) error {
	ctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	go func() {
		nextLog := r.progress.start.Add(10 * time.Second)
		ticker := time.NewTicker(r.cfg.Interval)
		defer ticker.Stop()
		currentTime := r.cfg.StartTime
		for i := 0; currentTime.UnixNano() < r.cfg.EndTime.UnixNano(); i++ {
			if ctx.Err() != nil {
				return
			}
			if time.Now().After(nextLog) {
				progressPct := currentTime.Sub(r.cfg.StartTime).Seconds() / r.cfg.EndTime.Sub(r.cfg.StartTime).Seconds()
				r.settings.Logger.Info("generating metrics progress",
					zap.Int("progress_percent", int(progressPct*100)),
					zap.String("eta", r.progress.eta(progressPct).Round(time.Second).String()),
					zap.Uint64("datapoints", r.progress.datapoints.Load()),
					zap.Float64("data_points_per_second", r.progress.dataPointsPerSecond()),
				)
				nextLog = nextLog.Add(10 * time.Second)
			}
			r.progress.datapoints.Add(r.produceMetrics(ctx, currentTime))
			r.applyChurn(i, currentTime)

			if r.cfg.RealTime {
				<-ticker.C
			}
			currentTime = currentTime.Add(r.cfg.Interval)
		}
		if r.cfg.ExitAfterEnd {
			// After the runner has finished generating metrics, we wait for the configured duration before exiting.
			if r.cfg.ExitAfterEndTimeout > 0 {
				r.settings.Logger.Info("finished generating metrics, waiting before exiting",
					zap.Duration("exit_after_end_timeout", r.cfg.ExitAfterEndTimeout),
				)
				time.Sleep(r.cfg.ExitAfterEndTimeout)
			} else {
				r.settings.Logger.Info("finished generating metrics, exiting immediately")
			}

			componentstatus.ReportStatus(host, componentstatus.NewFatalErrorEvent(errors.New("exiting because exit_after_end is set to true")))
		}
	}()

	return nil
}

func replaceHistogramsWithExponentialHistograms(m pmetric.Metric) {
	if m.Type() == pmetric.MetricTypeHistogram {
		// Get the histogram data
		histogram := m.Histogram()
		histogramDPs := histogram.DataPoints()

		// Store only the data point information declared in the DataPoint interface
		type dpInfo struct {
			startTimestamp pcommon.Timestamp
			timestamp      pcommon.Timestamp
			attributes     pcommon.Map
		}

		dpInfos := make([]dpInfo, histogramDPs.Len())
		for i := 0; i < histogramDPs.Len(); i++ {
			dp := histogramDPs.At(i)
			dpInfos[i] = dpInfo{
				startTimestamp: dp.StartTimestamp(),
				timestamp:      dp.Timestamp(),
				attributes:     dp.Attributes(),
			}
		}

		// Convert to exponential histogram
		expHist := m.SetEmptyExponentialHistogram()
		expHist.SetAggregationTemporality(histogram.AggregationTemporality())

		// Create exponential histogram data points preserving the series
		expDPs := expHist.DataPoints()
		expDPs.EnsureCapacity(len(dpInfos))

		for _, info := range dpInfos {
			expDP := expDPs.AppendEmpty()
			expDP.SetStartTimestamp(info.startTimestamp)
			expDP.SetTimestamp(info.timestamp)
			info.attributes.CopyTo(expDP.Attributes())
		}
	}

}

func addJitter(t time.Time, stdDev time.Duration, interval time.Duration, ra *rand.Rand) time.Time {
	if stdDev == 0 {
		return t
	}
	jitter := time.Duration(int64(math.Abs(ra.NormFloat64() * float64(stdDev))))
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

		startTime := simulatedTime.Format(time.RFC3339)
		for i := 0; i < scn.config.Churn; i++ {
			id := scn.config.Scale + interval*scn.config.Churn + i
			resource, err := metricstmpl.RenderResource(scn.config.Path, id, startTime, scn.config.TemplateVars, r.baseRand)
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

		// we don't keep track of the data points for each instance individually to reduce memory pressure
		// we still advance the metrics template have a new baseline that's used when simulating the metrics for each individual instance
		// this makes sure counters are increasing over time
		dp.ForEachDataPoint(scn.metricsTemplate, func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric, dp dp.DataPoint) {
			distribution.AdvanceDataPoint(dp, r.baseRand, m, r.cfg.Distribution, r.expHistoGen, scn.precision)
		})
		if scn.config.Concurrency == 0 {
			for i := range scn.config.Scale {
				*dataPoints += uint64(r.produceMetricsForInstance(ctx, r.baseRand, currentTime, scn, scn.resources[i]))
			}
			continue
		}

		for i := 0; i < scn.config.Concurrency; i++ {
			// Use a new random number generator for each goroutine to avoid race conditions
			rng := r.getNewRand()

			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < scn.config.Scale/scn.config.Concurrency; j++ {
					resource := scn.resources[j+i*scn.config.Scale/scn.config.Concurrency]
					currentDataPoints := r.produceMetricsForInstance(ctx, rng, currentTime, scn, resource)
					atomic.AddUint64(dataPoints, uint64(currentDataPoints))
				}
			}()
		}
	}
	wg.Wait()
	return *dataPoints
}

func (r *MetricsGenReceiver) produceMetricsForInstance(ctx context.Context, rng *rand.Rand, currentTime time.Time, scn Scenario, instanceResource pcommon.Resource) int {
	r.obsreport.StartMetricsOp(ctx)
	metrics := pmetric.NewMetrics()
	scn.metricsTemplate.CopyTo(metrics)
	resourceMetrics := metrics.ResourceMetrics()
	for j := 0; j < resourceMetrics.Len(); j++ {
		overrideExistingAttributes(instanceResource, resourceMetrics.At(j).Resource())
	}

	instanceTime := addJitter(currentTime, r.cfg.IntervalJitterStdDev, r.cfg.Interval, rng)

	dp.ForEachDataPoint(&metrics, func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric, dp dp.DataPoint) {
		distribution.AdvanceDataPoint(dp, rng, m, r.cfg.Distribution, r.expHistoGen, scn.precision)
		dp.SetTimestamp(pcommon.NewTimestampFromTime(instanceTime))
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
	r.settings.Logger.Info("finished generating metrics",
		zap.Uint64("datapoints", r.progress.datapoints.Load()),
		zap.String("duration", r.progress.duration().Round(time.Millisecond).String()),
		zap.Float64("data_points_per_second", r.progress.dataPointsPerSecond()),
	)
	return nil
}

// getNewRand returns a new random number generator seeded with the configured seed.
// This is NOT thread-safe, so it should only be used in a single goroutine.
func (r *MetricsGenReceiver) getNewRand() *rand.Rand {
	return rand.New(rand.NewSource(r.baseRand.Int63()))
}
