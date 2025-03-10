package metricsgenreceiver

import (
	"context"
	"errors"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/metricsgenreceiver/internal/distribution"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/metricsgenreceiver/internal/dp"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/metricsgenreceiver/internal/metadata"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/metricsgenreceiver/internal/metricstmpl"
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
	rand        *rand.Rand
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
	r := rand.New(rand.NewSource(cfg.Seed))
	nowish := time.Now().Truncate(time.Second)
	if cfg.StartTime.IsZero() {
		cfg.StartTime = nowish.Add(-cfg.StartNowMinus)
	}
	if cfg.EndTime.IsZero() {
		cfg.EndTime = nowish.Add(-cfg.EndNowMinus)
	}

	scenarios := make([]Scenario, 0, len(cfg.Scenarios))
	for _, scn := range cfg.Scenarios {

		metrics, err := metricstmpl.RenderMetricsTemplate(scn.Path, scn.TemplateVars)
		if err != nil {
			return nil, err
		}
		dp.ForEachDataPoint(&metrics, func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric, dp dp.DataPoint) {
			dp.SetStartTimestamp(pcommon.NewTimestampFromTime(cfg.StartTime))
		})
		resources, err := metricstmpl.GetResources(scn.Path, cfg.StartTime, scn.Scale, scn.TemplateVars, r)
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
		rand:      r,
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
			simulatedTime := currentTime
			if r.cfg.IntervalJitter {
				simulatedTime = addJitter(currentTime)
			}
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

func addJitter(t time.Time) time.Time {
	jitter := int64(math.Abs(rand.NormFloat64() * float64(5*time.Millisecond)))
	jitter = min(jitter, int64(20*time.Millisecond))
	return t.Add(time.Duration(jitter))
}

func (r *MetricsGenReceiver) applyChurn(interval int, simulatedTime time.Time) {
	for _, scn := range r.scenarios {
		if scn.config.Churn == 0 {
			continue
		}

		startTime := simulatedTime.Format(time.RFC3339)
		for i := 0; i < scn.config.Churn; i++ {
			id := scn.config.Scale + interval*scn.config.Churn + i
			resource, err := metricstmpl.RenderResource(scn.config.Path, id, startTime, scn.config.TemplateVars, r.rand)
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
			distribution.AdvanceDataPoint(dp, r.rand, m, r.cfg.Distribution)
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
	dp.ForEachDataPoint(&metrics, func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric, dp dp.DataPoint) {
		distribution.AdvanceDataPoint(dp, r.rand, m, r.cfg.Distribution)
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
