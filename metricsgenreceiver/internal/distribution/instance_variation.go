package distribution

import (
	"fmt"
	"math"
	"math/rand"
	"strings"

	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/dp"
	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/expohistogen"
	"go.opentelemetry.io/collector/pdata/pmetric"
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
	ApplyNumber(v pmetric.NumberDataPoint, instanceID int, identityHash uint64, metric pmetric.Metric, precision map[string]int)
}

var instanceVariations = map[GenerationHintClass]InstanceVariationStrategy{
	GenerationHintClock:         noInstanceVariation{},
	GenerationHintConstant:      noInstanceVariation{},
	GenerationHintStableBinary:  noInstanceVariation{},
	GenerationHintCurrentCount:  integerOffsetVariation{min: -2, max: 2},
	GenerationHintSlowGauge:     boundedMultiplierVariation{min: 0.95, max: 1.05, clampUtilization: true},
	GenerationHintSparseCounter: boundedMultiplierVariation{min: 0.95, max: 1.05},
	GenerationHintSteadyCounter: boundedMultiplierVariation{min: 0.95, max: 1.05},
}

// ApplyInstanceVariation applies deterministic per-instance variation to a number data point.
// If a hint is configured for the metric, its declared variation strategy is used; otherwise the
// variation is inferred from the metric shape. identityHash must be the value returned by
// IdentityHash for this data point.
func ApplyInstanceVariation(v pmetric.NumberDataPoint, instanceID int, identityHash uint64, metric pmetric.Metric, hints map[string]MetricGenerationHint, precision map[string]int) {
	strategyFor(metric, hints).ApplyNumber(v, instanceID, identityHash, metric, precision)
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
	Rand         *rand.Rand
	Dist         DistributionCfg
	ExpHistoGen  *expohistogen.Generator
}

// EmitForInstance is the single entry point the receiver uses to emit a data point for an
// instance. It hides the dispatch over data-point types so callers don't need to type-switch.
func EmitForInstance(dataPoint dp.DataPoint, metric pmetric.Metric, opts InstanceEmitOptions) {
	switch v := dataPoint.(type) {
	case pmetric.NumberDataPoint:
		ApplyInstanceVariation(v, opts.InstanceID, opts.IdentityHash, metric, opts.Hints, opts.Precision)
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
	case pmetric.MetricTypeGauge, pmetric.MetricTypeSum:
		return boundedMultiplierVariation{min: 0.95, max: 1.05, clampUtilization: true}
	default:
		return noInstanceVariation{}
	}
}

type noInstanceVariation struct{}

func (noInstanceVariation) ApplyNumber(pmetric.NumberDataPoint, int, uint64, pmetric.Metric, map[string]int) {
}

type boundedMultiplierVariation struct {
	min              float64
	max              float64
	clampUtilization bool
}

func (s boundedMultiplierVariation) ApplyNumber(v pmetric.NumberDataPoint, instanceID int, identityHash uint64, metric pmetric.Metric, precision map[string]int) {
	current := currentNumberDataPointValue(v)
	if current == 0 {
		return
	}
	multiplier := s.min + (s.max-s.min)*hashToUnitInterval(mixInstanceID(identityHash, instanceID))
	next := math.Max(0, current*multiplier)
	if s.clampUtilization && strings.HasSuffix(metric.Name(), ".utilization") {
		next = min(next, 1)
	}
	writeNumberDataPoint(v, next, metric, precision)
}

type integerOffsetVariation struct {
	min int64
	max int64
}

func (s integerOffsetVariation) ApplyNumber(v pmetric.NumberDataPoint, instanceID int, identityHash uint64, metric pmetric.Metric, precision map[string]int) {
	current := currentNumberDataPointValue(v)
	span := s.max - s.min + 1
	if span <= 0 {
		panic("invalid integer offset bounds")
	}
	base := int64(identityHash % uint64(span))
	offset := positiveMod(int64(instanceID)+base, span) + s.min
	next := math.Max(0, current+float64(offset))
	writeNumberDataPoint(v, next, metric, precision)
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
