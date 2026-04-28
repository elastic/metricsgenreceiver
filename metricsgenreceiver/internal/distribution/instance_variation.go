package distribution

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/dp"
	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/expohistogen"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

const (
	gaugeTemporalVariationAmplitude = 0.08
	currentCountTemporalOffsetMax   = 3
	counterExtraRateMax             = 0.20
	counterRateWaveAmplitude        = 0.80
	counterRateWavePeriodIntervals  = 20.0
	temporalVariationBucketCount    = 10

	minGaugeMultiplier   = 1.00
	maxGaugeMultiplier   = 2.00
	minCounterMultiplier = 0.90
	maxCounterMultiplier = 1.10
)

// InstanceVariationStrategy applies deterministic per-instance variation to a number data point.
// It is only invoked during the per-instance emission pass, once per instance per data point per
// interval — after the shared template has already been advanced via GenerationHintStrategy. The
// template advance pass does not call ApplyNumber.
//
// The variation is a stable function of (instanceID, metric name, data-point attributes) — it does
// not introduce random walk at emission time, which keeps each instance's series coherent over
// successive intervals. Strategies receive a precomputed identityHash for (metric name, attributes)
// so they can avoid re-hashing the attribute set on every data-point emission; see IdentityHash.
//
// Implementations must be stateless: keeping per-series state would violate the project's
// bounded-memory design principle.
type InstanceVariationStrategy interface {
	ApplyNumber(v pmetric.NumberDataPoint, instanceID int, identityHash uint64, metric pmetric.Metric, precision map[string]int, opts InstanceVariationOptions)
}

var instanceVariations = map[GenerationHintClass]InstanceVariationStrategy{
	GenerationHintClock:         noInstanceVariation{},
	GenerationHintConstant:      noInstanceVariation{},
	GenerationHintStableBinary:  noInstanceVariation{},
	GenerationHintCurrentCount:  currentCountVariation{minOffset: -2, maxOffset: 2},
	GenerationHintSlowGauge:     gaugeVariation(),
	GenerationHintSparseCounter: counterVariation(),
	GenerationHintSteadyCounter: counterVariation(),
}

// ApplyInstanceVariation applies deterministic per-instance variation to a number data point.
// If a hint is configured for the metric, its declared variation strategy is used; otherwise the
// variation is inferred from the metric shape. identityHash must be the value returned by
// IdentityHash for this data point.
func ApplyInstanceVariation(v pmetric.NumberDataPoint, instanceID int, identityHash uint64, metric pmetric.Metric, hints map[string]MetricGenerationHint, precision map[string]int, opts InstanceVariationOptions) {
	strategyFor(metric, hints).ApplyNumber(v, instanceID, identityHash, metric, precision, opts)
}

// InstanceVariationOptions contains timing information used for stateless,
// time-varying per-instance variation.
type InstanceVariationOptions struct {
	Timestamp        time.Time
	StartTime        time.Time
	Interval         time.Duration
	CounterBaseDelta float64
}

// InstanceEmitOptions carries the state needed to emit a data point for one specific instance.
// NumberDataPoints go through the stable per-series variation path (InstanceID + IdentityHash +
// Hints); other data-point types (histograms, summaries) fall back to the randomised advance and
// use Rand / Dist / ExpHistoGen.
type InstanceEmitOptions struct {
	InstanceID   int
	IdentityHash uint64
	Hints        map[string]MetricGenerationHint
	Precision    map[string]int
	Variation    InstanceVariationOptions
	Rand         *rand.Rand
	Dist         DistributionCfg
	ExpHistoGen  *expohistogen.Generator
}

// EmitForInstance is the single entry point the receiver uses to emit a data point for an
// instance. It hides the dispatch over data-point types so callers don't need to type-switch.
func EmitForInstance(dataPoint dp.DataPoint, metric pmetric.Metric, opts InstanceEmitOptions) {
	switch v := dataPoint.(type) {
	case pmetric.NumberDataPoint:
		ApplyInstanceVariation(v, opts.InstanceID, opts.IdentityHash, metric, opts.Hints, opts.Precision, opts.Variation)
	default:
		AdvanceDataPoint(dataPoint, opts.Rand, metric, opts.Dist, opts.ExpHistoGen, AdvanceOptions{Precision: opts.Precision})
	}
}

func strategyFor(metric pmetric.Metric, hints map[string]MetricGenerationHint) InstanceVariationStrategy {
	if hint, ok := hints[metric.Name()]; ok {
		strategy, ok := instanceVariations[hint.Class]
		if !ok {
			panic(fmt.Sprintf("unsupported generation hint class %q", hint.Class))
		}
		return strategy
	}
	return defaultInstanceVariationFor(metric)
}

func defaultInstanceVariationFor(metric pmetric.Metric) InstanceVariationStrategy {
	switch metric.Type() {
	case pmetric.MetricTypeGauge:
		return gaugeVariation()
	case pmetric.MetricTypeSum:
		return counterVariation()
	default:
		return noInstanceVariation{}
	}
}

type noInstanceVariation struct{}

func (noInstanceVariation) ApplyNumber(pmetric.NumberDataPoint, int, uint64, pmetric.Metric, map[string]int, InstanceVariationOptions) {
}

type boundedMultiplierVariation struct {
	min                         float64
	max                         float64
	autoBounceBetweenZeroAndOne bool
}

func gaugeVariation() boundedMultiplierVariation {
	return boundedMultiplierVariation{
		min:                         minGaugeMultiplier,
		max:                         maxGaugeMultiplier,
		autoBounceBetweenZeroAndOne: true,
	}
}

func counterVariation() boundedMultiplierVariation {
	return boundedMultiplierVariation{
		min: minCounterMultiplier,
		max: maxCounterMultiplier,
	}
}

func (s boundedMultiplierVariation) ApplyNumber(v pmetric.NumberDataPoint, instanceID int, identityHash uint64, metric pmetric.Metric, precision map[string]int, opts InstanceVariationOptions) {
	current := currentNumberDataPointValue(v)
	if current == 0 {
		return
	}
	instanceUnit := hashToUnitInterval(mixInstanceID(identityHash, instanceID))
	var next float64
	if isCumulativeMonotonicSum(metric) {
		next = s.applyCumulativeCounterVariation(current, identityHash, instanceID, instanceUnit, opts)
	} else {
		next = s.applyNonCumulativeVariation(current, identityHash, instanceID, instanceUnit, opts)
	}
	writeNumberDataPoint(v, next, metric, precision)
}

func (s boundedMultiplierVariation) applyCumulativeCounterVariation(current float64, identityHash uint64, instanceID int, instanceUnit float64, opts InstanceVariationOptions) float64 {
	next := current * stableMultiplier(s.min, s.max, instanceUnit)
	next += counterRateOffset(identityHash, instanceID, instanceUnit, opts)
	return math.Max(0, next)
}

func (s boundedMultiplierVariation) applyNonCumulativeVariation(current float64, identityHash uint64, instanceID int, instanceUnit float64, opts InstanceVariationOptions) float64 {
	isUnitInterval := isUnitIntervalGaugeValue(current)
	next := current * stableMultiplier(s.min, s.max, instanceUnit)
	next *= temporalMultiplier(identityHash, instanceID, opts, isUnitInterval)
	next = math.Max(0, next)
	if s.autoBounceBetweenZeroAndOne && isUnitInterval {
		next = bounceBetweenZeroAndOne(next)
	}
	return next
}

type currentCountVariation struct {
	minOffset int64
	maxOffset int64
}

func (s currentCountVariation) ApplyNumber(v pmetric.NumberDataPoint, instanceID int, identityHash uint64, metric pmetric.Metric, precision map[string]int, opts InstanceVariationOptions) {
	current := currentNumberDataPointValue(v)
	span := s.maxOffset - s.minOffset + 1
	if span <= 0 {
		panic("invalid integer offset bounds")
	}
	base := int64(identityHash % uint64(span))
	offset := positiveMod(int64(instanceID)+base, span) + s.minOffset
	offset += int64(math.Round(currentCountTemporalOffsetMax * temporalNoise(identityHash, instanceID, opts)))
	instanceUnit := hashToUnitInterval(mixInstanceID(identityHash, instanceID))
	multiplier := stableGaugeMultiplier(instanceUnit)
	next := math.Max(0, current*multiplier+float64(offset))
	writeNumberDataPoint(v, next, metric, precision)
}

func stableGaugeMultiplier(instanceUnit float64) float64 {
	// Keep gauge multipliers >= 1 so non-unit gauges do not get pushed below 1
	// and accidentally become eligible for [0,1] bouncing on later intervals.
	return stableMultiplier(minGaugeMultiplier, maxGaugeMultiplier, instanceUnit)
}

func stableMultiplier(minValue, maxValue, unit float64) float64 {
	return minValue + (maxValue-minValue)*unit
}

func isUnitIntervalGaugeValue(v float64) bool {
	return v >= 0 && v <= 1
}

func isCumulativeMonotonicSum(metric pmetric.Metric) bool {
	return metric.Type() == pmetric.MetricTypeSum &&
		metric.Sum().IsMonotonic() &&
		metric.Sum().AggregationTemporality() == pmetric.AggregationTemporalityCumulative
}

func temporalMultiplier(identityHash uint64, instanceID int, opts InstanceVariationOptions, allowNegativeNoise bool) float64 {
	if opts.Timestamp.IsZero() || opts.Interval <= 0 {
		return 1
	}
	noise := temporalNoise(identityHash, instanceID, opts)
	if !allowNegativeNoise {
		noise = 0.5 + 0.5*noise
	}
	return 1 + gaugeTemporalVariationAmplitude*noise
}

func counterRateOffset(identityHash uint64, instanceID int, instanceUnit float64, opts InstanceVariationOptions) float64 {
	if opts.Timestamp.IsZero() || opts.StartTime.IsZero() || opts.Interval <= 0 {
		return 0
	}
	elapsed := opts.Timestamp.Sub(opts.StartTime).Seconds() / opts.Interval.Seconds()
	if elapsed <= 0 {
		return 0
	}
	phase := 2 * math.Pi * hashToUnitInterval(mixInstanceID(identityHash^0x94d049bb133111eb, instanceID))
	wave := integratedCounterRateWave(elapsed, phase)
	return opts.CounterBaseDelta * counterExtraRateMax * instanceUnit * wave
}

func integratedCounterRateWave(elapsed float64, phase float64) float64 {
	// Integrate a positive rate wave so cumulative counters stay monotonic while
	// rate() views still see time-varying per-instance movement.
	angularFrequency := 2 * math.Pi / counterRateWavePeriodIntervals
	return elapsed + counterRateWaveAmplitude/angularFrequency*(math.Sin(angularFrequency*elapsed+phase)-math.Sin(phase))
}

func temporalNoise(identityHash uint64, instanceID int, opts InstanceVariationOptions) float64 {
	if opts.Timestamp.IsZero() || opts.Interval <= 0 {
		return 0
	}
	bucketDuration := opts.Interval * temporalVariationBucketCount
	if bucketDuration <= 0 {
		return 0
	}
	timestamp := opts.Timestamp.UnixNano()
	bucketNanos := bucketDuration.Nanoseconds()
	bucket := timestamp / bucketNanos
	fraction := float64(timestamp%bucketNanos) / float64(bucketNanos)
	if fraction < 0 {
		fraction += 1
		bucket--
	}
	start := hashToSignedUnit(temporalBucketHash(identityHash, instanceID, bucket))
	end := hashToSignedUnit(temporalBucketHash(identityHash, instanceID, bucket+1))
	return lerp(start, end, smoothstep(fraction))
}

func temporalBucketHash(identityHash uint64, instanceID int, bucket int64) uint64 {
	return mixInstanceID(identityHash^uint64(bucket)*0xbf58476d1ce4e5b9, instanceID)
}

func hashToSignedUnit(hash uint64) float64 {
	return hashToUnitInterval(hash)*2 - 1
}

func smoothstep(v float64) float64 {
	return v * v * (3 - 2*v)
}

func lerp(start, end, fraction float64) float64 {
	return start + (end-start)*fraction
}

func writeNumberDataPoint(v pmetric.NumberDataPoint, next float64, metric pmetric.Metric, precision map[string]int) {
	switch v.ValueType() {
	case pmetric.NumberDataPointValueTypeDouble:
		if p, ok := precision[metric.Name()]; ok {
			next = roundToPrecision(next, p)
		}
		v.SetDoubleValue(next)
	case pmetric.NumberDataPointValueTypeInt:
		v.SetIntValue(int64(math.Max(0, math.Round(next))))
	}
}

func positiveMod(v int64, mod int64) int64 {
	r := v % mod
	if r < 0 {
		r += mod
	}
	return r
}
