package metricsgenreceiver

import (
	"bytes"
	"context"
	"errors"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/metricsgenreceiver/internal/metadata"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/metricsgenreceiver/internal/resourceattr"
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
	"path/filepath"
	"sync"
	"sync/atomic"
	"text/template"
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

		buf, err := renderMetricsTemplate(scn, err)
		if err != nil {
			return nil, err
		}

		metricsUnmarshaler := &pmetric.JSONUnmarshaler{}
		metrics, err := metricsUnmarshaler.UnmarshalMetrics(buf.Bytes())
		forEachDataPoint(&metrics, func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric, dp dataPoint) {
			dp.SetStartTimestamp(pcommon.NewTimestampFromTime(cfg.StartTime))
		})
		if err != nil {
			return nil, err
		}

		resources, err := resourceattr.GetResources(scn.Path, cfg.StartTime, scn.Scale, r)
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

func renderMetricsTemplate(scn ScenarioCfg, err error) (*bytes.Buffer, error) {
	funcMap := template.FuncMap{
		"loop": func(from, to int) <-chan int {
			ch := make(chan int)
			go func() {
				for i := from; i <= to; i++ {
					ch <- i
				}
				close(ch)
			}()
			return ch
		},
	}
	path := scn.Path
	path += ".json"
	tpl, err := template.New(path).Funcs(funcMap).ParseFiles(path)
	if err != nil {
		return nil, err
	}

	buf := new(bytes.Buffer)
	err = tpl.ExecuteTemplate(buf, filepath.Base(path), scn.TemplateVars)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func (r *MetricsGenReceiver) Start(ctx context.Context, host component.Host) error {
	ctx = context.Background()
	ctx, r.cancel = context.WithCancel(ctx)
	go func() {
		start := time.Now()
		ticker := time.NewTicker(r.cfg.Interval)
		defer ticker.Stop()
		dataPoints := uint64(0)
		currentTime := r.cfg.StartTime
		for i := 0; currentTime.UnixNano() <= r.cfg.EndTime.UnixNano(); i++ {
			if ctx.Err() != nil {
				return
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
			return
		}

		startTime := simulatedTime.Format(time.RFC3339)
		for i := 0; i < scn.config.Churn; i++ {
			id := scn.config.Scale + interval*scn.config.Churn + i
			resource := scn.resources[id%len(scn.resources)]
			resourceattr.RenderResourceAttributes(scn.resourceAttributesTemplate, resource, id, startTime, r.rand)
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
		forEachDataPoint(scn.metricsTemplate, func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric, dp dataPoint) {
			advanceDataPoint(dp, r.rand, m, r.cfg.Distribution)
		})
		for i := 0; i < scn.config.Scale; i++ {
			wg.Add(1)
			f := func() {
				defer wg.Done()
				currentDataPoints := r.produceMetricsForInstance(ctx, currentTime, scn, i)
				atomic.AddUint64(dataPoints, uint64(currentDataPoints))
			}
			if scn.config.ConcurrentInstances {
				go f()
			} else {
				f()
			}
		}
	}
	wg.Wait()
	return *dataPoints
}

func (r *MetricsGenReceiver) produceMetricsForInstance(ctx context.Context, currentTime time.Time, scn Scenario, i int) int {
	r.obsreport.StartMetricsOp(ctx)
	metrics := pmetric.NewMetrics()
	scn.metricsTemplate.CopyTo(metrics)
	for j := 0; j < metrics.ResourceMetrics().Len(); j++ {
		ra := metrics.ResourceMetrics().At(j).Resource().Attributes()
		scn.resources[i].Attributes().Range(func(k string, v pcommon.Value) bool {
			if _, exists := ra.Get(k); exists {
				targetValue := ra.PutEmpty(k)
				v.CopyTo(targetValue)
			}
			return true
		})
	}
	forEachDataPoint(&metrics, func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric, dp dataPoint) {
		advanceDataPoint(dp, r.rand, m, r.cfg.Distribution)
		dp.SetTimestamp(pcommon.NewTimestampFromTime(currentTime))
	})
	err := r.nextMetrics.ConsumeMetrics(ctx, metrics)
	dataPoints := metrics.DataPointCount()
	r.obsreport.EndMetricsOp(ctx, metadata.Type.String(), dataPoints, err)
	return dataPoints
}

func advanceDataPoint(dp dataPoint, rand *rand.Rand, m pmetric.Metric, dist DistributionCfg) {
	switch v := dp.(type) {
	case pmetric.NumberDataPoint:
		switch v.ValueType() {
		case pmetric.NumberDataPointValueTypeDouble:
			value := v.DoubleValue()
			if m.Type() == pmetric.MetricTypeGauge {
				if value >= 0 && value <= 1 {
					value = advanceZeroToOne(value, rand, dist)
				} else {
					value = advanceFloat(rand, m, value, dist)
					// avoid keeping the value locked between 0..1 in successive runs
					if value >= 0 && value <= 1 {
						value += 1.1
					}
				}
			} else {
				value = advanceFloat(rand, m, value, dist)
			}
			v.SetDoubleValue(value)
			break
		case pmetric.NumberDataPointValueTypeInt:
			v.SetIntValue(advanceInt(rand, m, v.IntValue(), dist))
			break
		default:
		}
	}
}

func advanceZeroToOne(value float64, rand *rand.Rand, dist DistributionCfg) float64 {
	value += rand.NormFloat64() * dist.StdDevGaugePct
	// keep locked between 0..1
	value = math.Abs(value)
	value = min(value, 1)
	return value
}

func advanceInt(rand *rand.Rand, m pmetric.Metric, value int64, dist DistributionCfg) int64 {
	return int64(advanceFloat(rand, m, float64(value), dist))
}

func advanceFloat(rand *rand.Rand, m pmetric.Metric, value float64, dist DistributionCfg) float64 {
	delta := rand.NormFloat64() * dist.StdDev
	if isMonotonicSum(&m) {
		delta += float64(dist.MedianMonotonicSum)
	}
	if isDelta(&m) {
		value = delta
	} else {
		value += delta
	}
	return value
}

func isMonotonicSum(m *pmetric.Metric) bool {
	return m.Type() == pmetric.MetricTypeSum && m.Sum().IsMonotonic()
}

func isDelta(m *pmetric.Metric) bool {
	return m.Type() == pmetric.MetricTypeSum && m.Sum().AggregationTemporality() == pmetric.AggregationTemporalityDelta
}

func forEachDataPoint(ms *pmetric.Metrics, visitor func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric, dp dataPoint)) {
	rms := ms.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		rm := rms.At(i)
		ilms := rm.ScopeMetrics()
		for j := 0; j < ilms.Len(); j++ {
			sm := ilms.At(j)
			metricsList := sm.Metrics()
			for k := 0; k < metricsList.Len(); k++ {
				m := metricsList.At(k)
				//exhaustive:enforce
				switch metricsList.At(k).Type() {
				case pmetric.MetricTypeGauge:
					ds := m.Gauge().DataPoints()
					for l := 0; l < ds.Len(); l++ {
						visitor(rm.Resource(), sm.Scope(), m, ds.At(l))
					}
				case pmetric.MetricTypeSum:
					ds := m.Sum().DataPoints()
					for l := 0; l < ds.Len(); l++ {
						visitor(rm.Resource(), sm.Scope(), m, ds.At(l))
					}
				case pmetric.MetricTypeHistogram:
					ds := m.Histogram().DataPoints()
					for l := 0; l < ds.Len(); l++ {
						visitor(rm.Resource(), sm.Scope(), m, ds.At(l))
					}
				case pmetric.MetricTypeExponentialHistogram:
					ds := m.ExponentialHistogram().DataPoints()
					for l := 0; l < ds.Len(); l++ {
						visitor(rm.Resource(), sm.Scope(), m, ds.At(l))
					}
				case pmetric.MetricTypeSummary:
					ds := m.Summary().DataPoints()
					for l := 0; l < ds.Len(); l++ {
						visitor(rm.Resource(), sm.Scope(), m, ds.At(l))
					}
				case pmetric.MetricTypeEmpty:
				}
			}
		}
	}
}

type dataPoint interface {
	Attributes() pcommon.Map
	StartTimestamp() pcommon.Timestamp
	SetStartTimestamp(pcommon.Timestamp)
	Timestamp() pcommon.Timestamp
	SetTimestamp(pcommon.Timestamp)
}

func (r *MetricsGenReceiver) Shutdown(_ context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	return nil
}
