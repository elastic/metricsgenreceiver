package distribution

import (
	"math"
	"math/rand"
	"strconv"
	"strings"

	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/dp"
	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/expohistogen"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

var DefaultDistribution = DistributionCfg{
	MedianMonotonicSum: 100,
	StdDevGaugePct:     0.01,
	StdDev:             1.0,
}

type DistributionCfg struct {
	MedianMonotonicSum uint    `mapstructure:"median_monotonic_sum"`
	StdDevGaugePct     float64 `mapstructure:"std_dev_gauge_pct"`
	StdDev             float64 `mapstructure:"std_dev"`
}

func InferPrecision(metrics *pmetric.Metrics) map[string]int {
	precision := make(map[string]int)
	dp.ForEachDataPoint(metrics, func(_ pcommon.Resource, _ pcommon.InstrumentationScope, m pmetric.Metric, d dp.DataPoint) {
		ndp, ok := d.(pmetric.NumberDataPoint)
		if !ok || ndp.ValueType() != pmetric.NumberDataPointValueTypeDouble {
			return
		}
		v := ndp.DoubleValue()
		if v == 0 {
			return
		}
		places := decimalPlaces(v)
		if current, exists := precision[m.Name()]; !exists || places > current {
			precision[m.Name()] = places
		}
	})
	return precision
}

func decimalPlaces(v float64) int {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	i := strings.IndexByte(s, '.')
	if i < 0 {
		return 0
	}
	return len(s) - i - 1
}

func roundToPrecision(v float64, decimals int) float64 {
	mul := math.Pow(10, float64(decimals))
	return math.Round(v*mul) / mul
}

func AdvanceDataPoint(dp dp.DataPoint, r *rand.Rand, m pmetric.Metric, dist DistributionCfg, expHistoGen *expohistogen.Generator, precision map[string]int) {

	switch v := dp.(type) {
	case pmetric.NumberDataPoint:
		switch v.ValueType() {
		case pmetric.NumberDataPointValueTypeDouble:
			value := v.DoubleValue()
			if m.Type() == pmetric.MetricTypeGauge {
				if value >= 0 && value <= 1 {
					value = advanceZeroToOne(value, r, dist)
				} else {
					value = advanceFloat(r, m, value, dist)
					// avoid keeping the value locked between 0..1 in successive runs
					if value >= 0 && value <= 1 {
						if value < 0.5 {
							value--
						} else {
							value++
						}
					}
				}
			} else {
				value = advanceFloat(r, m, value, dist)
			}
			if p, ok := precision[m.Name()]; ok {
				value = roundToPrecision(value, p)
			}
			v.SetDoubleValue(value)
			break
		case pmetric.NumberDataPointValueTypeInt:
			v.SetIntValue(advanceInt(r, m, v.IntValue(), dist))
			break
		default:
		}
	case pmetric.HistogramDataPoint:
		count := uint64(0)
		for i := 0; i < v.BucketCounts().Len(); i++ {
			val := uint64(advanceInt(r, m, int64(v.BucketCounts().At(i)), dist))
			count += val
			v.BucketCounts().SetAt(i, val)
		}
		v.SetCount(count)
		v.RemoveSum()
	case pmetric.ExponentialHistogramDataPoint:
		if m.ExponentialHistogram().AggregationTemporality() == pmetric.AggregationTemporalityCumulative {
			panic("Cumulative exponential histograms not supported")
		}
		expHistoGen.GenerateInto(r, v)
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
	vf := advanceFloat(rand, m, float64(value), dist)
	vi := int64(vf)
	// probabilistic rounding
	if vf-float64(vi) > rand.Float64() {
		vi++
	}
	return vi
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
	// negative metrics are pretty rare, so we just simulate positive values
	return math.Abs(value)
}

func isMonotonicSum(m *pmetric.Metric) bool {
	return m.Type() == pmetric.MetricTypeSum && m.Sum().IsMonotonic()
}

func isDelta(m *pmetric.Metric) bool {
	return m.Type() == pmetric.MetricTypeSum && m.Sum().AggregationTemporality() == pmetric.AggregationTemporalityDelta
}
