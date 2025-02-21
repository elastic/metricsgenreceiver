package metricsgenreceiver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/metricsgenreceiver/internal/metadata"
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
	"net"
	"path/filepath"
	"reflect"
	"text/template"
	"time"
)

type MetricsGenReceiver struct {
	cfg       *Config
	obsreport *receiverhelper.ObsReport
	settings  receiver.Settings

	nextMetrics     consumer.Metrics
	metricsTemplate *pmetric.Metrics
	rand            *rand.Rand
	resources       []pcommon.Resource
	cancel          context.CancelFunc
}

func newMetricsGenReceiver(cfg *Config, set receiver.Settings) (*MetricsGenReceiver, error) {
	obsreport, err := receiverhelper.NewObsReport(receiverhelper.ObsReportSettings{
		ReceiverID:             set.ID,
		ReceiverCreateSettings: set,
	})
	if err != nil {
		return nil, err
	}

	buf, err := renderMetricsTemplate(cfg, err)
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

	r := rand.New(rand.NewSource(cfg.Seed))
	resources, err := renderResources(cfg, r)
	if err != nil {
		return nil, err
	}

	return &MetricsGenReceiver{
		cfg:             cfg,
		settings:        set,
		obsreport:       obsreport,
		metricsTemplate: &metrics,
		rand:            r,
		resources:       resources,
	}, nil
}

func renderMetricsTemplate(cfg *Config, err error) (*bytes.Buffer, error) {
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
	tpl, err := template.New(cfg.Path).Funcs(funcMap).ParseFiles(cfg.Path)
	if err != nil {
		return nil, err
	}

	buf := new(bytes.Buffer)
	err = tpl.ExecuteTemplate(buf, filepath.Base(cfg.Path), cfg.TemplateVars)
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
		dataPoints := 0
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
			r.applyChurn(i)

			if r.cfg.RealTime {
				<-ticker.C
			}
			currentTime = currentTime.Add(r.cfg.Interval)
		}
		duration := time.Now().Sub(start)

		r.settings.Logger.Info("finished generating metrics",
			zap.Int("datapoints", dataPoints),
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

func (r *MetricsGenReceiver) applyChurn(interval int) {
	if r.cfg.Churn == 0 {
		return
	}
	for i := 0; i < r.cfg.Churn; i++ {
		id := r.cfg.Scale + interval*r.cfg.Churn + i
		resource := r.resources[id%len(r.resources)]
		_ = renderResourceAttributes(r.cfg, resource, &resourceTemplateModel{
			ID:   id,
			rand: r.rand,
		})
	}
}

func (r *MetricsGenReceiver) produceMetrics(ctx context.Context, currentTime time.Time) int {
	forEachDataPoint(r.metricsTemplate, func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric, dp dataPoint) {
		advanceDataPoint(dp, r.rand, m)
	})
	dataPoints := 0
	for i := 0; i < r.cfg.Scale; i++ {
		r.obsreport.StartMetricsOp(ctx)
		metrics := pmetric.NewMetrics()
		r.metricsTemplate.CopyTo(metrics)
		for j := 0; j < metrics.ResourceMetrics().Len(); j++ {
			r.resources[i].CopyTo(metrics.ResourceMetrics().At(j).Resource())
		}
		forEachDataPoint(&metrics, func(res pcommon.Resource, is pcommon.InstrumentationScope, m pmetric.Metric, dp dataPoint) {
			advanceDataPoint(dp, r.rand, m)
			dp.SetTimestamp(pcommon.NewTimestampFromTime(currentTime))
		})
		metrics.MarkReadOnly()
		err := r.nextMetrics.ConsumeMetrics(ctx, metrics)
		currentCount := metrics.DataPointCount()
		r.obsreport.EndMetricsOp(ctx, metadata.Type.String(), dataPoints, err)
		dataPoints += currentCount
	}
	return dataPoints
}

func advanceDataPoint(dp dataPoint, rand *rand.Rand, m pmetric.Metric) {
	switch v := dp.(type) {
	case pmetric.NumberDataPoint:
		switch v.ValueType() {
		case pmetric.NumberDataPointValueTypeDouble:
			value := v.DoubleValue()
			if value >= 0 && value <= 1 {
				value = advanceZeroToOne(value, rand)
			} else {
				value = advanceFloat(rand, m, value)
				// avoid keeping the value locked between 0..1 in successive runs
				if value >= 0 && value <= 1 {
					value += 1.1
				}
			}
			v.SetDoubleValue(value)
			break
		case pmetric.NumberDataPointValueTypeInt:
			v.SetIntValue(advanceInt(rand, m, v.IntValue()))
			break
		default:
		}
	}
}

func advanceZeroToOne(value float64, rand *rand.Rand) float64 {
	value += rand.NormFloat64() * 0.05
	// keep locked between 0..1
	value = math.Abs(value)
	value = min(value, 1)
	return value
}

func advanceInt(rand *rand.Rand, m pmetric.Metric, value int64) int64 {
	return int64(advanceFloat(rand, m, float64(value)))
}

func advanceFloat(rand *rand.Rand, m pmetric.Metric, value float64) float64 {
	const median = 100
	const stddev = 5.0
	delta := rand.NormFloat64()*stddev + median
	delta = max(0, min(delta, median*2))
	if !isMonotonic(&m) {
		delta -= median
	}
	if isCumulative(&m) {
		value += delta
	} else {
		value = delta
	}
	return value
}

func isMonotonic(m *pmetric.Metric) bool {
	return m.Type() == pmetric.MetricTypeSum && m.Sum().IsMonotonic()
}

func isCumulative(m *pmetric.Metric) bool {
	return m.Type() == pmetric.MetricTypeSum && m.Sum().AggregationTemporality() == pmetric.AggregationTemporalityCumulative
}
func renderResources(cfg *Config, r *rand.Rand) ([]pcommon.Resource, error) {
	resources := make([]pcommon.Resource, cfg.Scale)
	for i := 0; i < cfg.Scale; i++ {
		resource := pcommon.NewResource()
		resources[i] = resource
		err := renderResourceAttributes(cfg, resource, &resourceTemplateModel{
			ID:   i,
			rand: r,
		})
		if err != nil {
			return nil, err
		}
	}
	return resources, nil
}

type resourceTemplateModel struct {
	ID int

	rand *rand.Rand
}

func (t *resourceTemplateModel) RandomIP() string {
	return net.IPv4(t.randByte(), t.randByte(), t.randByte(), t.randByte()).String()
}

func (t *resourceTemplateModel) RandomMAC() string {
	var mac net.HardwareAddr
	// Set the local bit
	mac = append(mac, t.randByte()|2, t.randByte(), t.randByte(), t.randByte(), t.randByte())
	return mac.String()
}
func (t *resourceTemplateModel) randByte() byte {
	return byte(t.rand.Int())
}

func renderResourceAttributes(c *Config, resource pcommon.Resource, model *resourceTemplateModel) error {
	attr := resource.Attributes()
	for k, v := range c.ResourceAttributes {
		kind := reflect.ValueOf(v).Kind()
		switch kind {
		case reflect.String:
			rendered, err := processResourceAttributeTemplate(k, v.(string), model)
			if err != nil {
				return err
			}
			attr.PutStr(k, rendered)
			break
		case reflect.Slice:
			slice := attr.PutEmptySlice(k)
			err := slice.FromRaw(v.([]any))
			for j := 0; j < slice.Len(); j++ {
				v := slice.At(j)
				if v.Type() == pcommon.ValueTypeStr {
					rendered, err := processResourceAttributeTemplate(k, v.Str(), model)
					if err != nil {
						return err
					}
					v.SetStr(rendered)
				}
			}
			if err != nil {
				return err
			}
			break
		default:
			return fmt.Errorf("unhandled resource attribute type %s: %s", k, kind)
		}
	}
	return nil
}

func processResourceAttributeTemplate(k, v string, model *resourceTemplateModel) (string, error) {
	tmpl, err := template.New(k).Parse(v)
	if err != nil {
		return "", err
	}
	buf := new(bytes.Buffer)
	err = tmpl.Execute(buf, model)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
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
