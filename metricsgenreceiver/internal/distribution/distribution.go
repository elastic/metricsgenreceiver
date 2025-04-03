package distribution

import (
	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/dp"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"math"
	"math/rand"
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

func AdvanceDataPoint(dp dp.DataPoint, rand *rand.Rand, m pmetric.Metric, dist DistributionCfg) {
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
						if value < 0.5 {
							value--
						} else {
							value++
						}
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
	case pmetric.HistogramDataPoint:
		count := uint64(0)
		for i := 0; i < v.BucketCounts().Len(); i++ {
			val := uint64(advanceInt(rand, m, int64(v.BucketCounts().At(i)), dist))
			count += val
			v.BucketCounts().SetAt(i, val)
		}
		v.SetCount(count)
		v.RemoveSum()
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
