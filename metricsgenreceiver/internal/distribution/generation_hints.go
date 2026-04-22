package distribution

import (
	"fmt"
	"math"
	"math/rand"
	"strings"

	"go.opentelemetry.io/collector/pdata/pmetric"
)

type GenerationHintClass string

const (
	// GenerationHintClock advances a time-like metric deterministically with the collection interval.
	// Example: node_time_seconds.
	GenerationHintClock         GenerationHintClass = "clock"
	// GenerationHintConstant keeps a metric fixed after its initial seed value.
	// Examples: node_memory_MemTotal_bytes, node_boot_time_seconds.
	GenerationHintConstant      GenerationHintClass = "constant"
	// GenerationHintCurrentCount models a non-negative integer count that changes in small steps.
	// Examples: system.network.connections, node_sockstat_TCP_inuse.
	GenerationHintCurrentCount  GenerationHintClass = "current_count"
	// GenerationHintSlowGauge models a gauge that moves gradually instead of wandering wildly between intervals.
	// Examples: system.cpu.utilization, system.memory.usage.
	GenerationHintSlowGauge     GenerationHintClass = "slow_gauge"
	// GenerationHintSparseCounter models a counter-like series that is flat most intervals and only increases occasionally.
	// Examples: system.network.errors, node_vmstat_oom_kill.
	GenerationHintSparseCounter GenerationHintClass = "sparse_counter"
	// GenerationHintStableBinary keeps a metric strictly at 0 or 1 and never flips after the seed is normalized.
	// Examples: node_network_up, node_filesystem_readonly.
	GenerationHintStableBinary  GenerationHintClass = "stable_binary"
	// GenerationHintSteadyCounter models a counter-like series that increases at a fairly stable rate most intervals.
	// Examples: system.cpu.time, node_network_receive_bytes_total.
	GenerationHintSteadyCounter GenerationHintClass = "steady_counter"
)

// MetricGenerationHint optionally overrides the default evolution model for a metric family.
type MetricGenerationHint struct {
	Class GenerationHintClass `json:"class" yaml:"class"`
}

// GenerationHintStrategy evolves a metric family on the shared template at each interval tick.
// It is only invoked during the template advance pass, once per data point per interval. The
// per-instance emission pass does not call Advance — instance-level variation is handled by
// InstanceVariationStrategy, which layers a deterministic offset or multiplier on top of the
// value Advance produced.
//
// Implementations should be pure functions of the arguments: given the same current value and
// options, successive calls are expected to produce a well-behaved random walk (or deterministic
// progression, for clock/constant/stable_binary). They must not keep per-series state, which
// would violate the project's bounded-memory design principle.
type GenerationHintStrategy interface {
	Advance(current float64, rand *rand.Rand, metric pmetric.Metric, dist DistributionCfg, opts AdvanceOptions) float64
}

var strategies = map[GenerationHintClass]GenerationHintStrategy{
	GenerationHintClock:         clockStrategy{},
	GenerationHintConstant:      constantStrategy{},
	GenerationHintCurrentCount:  currentCountStrategy{},
	GenerationHintSlowGauge:     slowGaugeStrategy{},
	GenerationHintSparseCounter: sparseCounterStrategy{},
	GenerationHintStableBinary:  stableBinaryStrategy{},
	GenerationHintSteadyCounter: steadyCounterStrategy{},
}

func (h MetricGenerationHint) Validate(metricName string) error {
	if h.Class == "" {
		return fmt.Errorf("metric generation hint for %q is missing a class", metricName)
	}
	if _, ok := strategies[h.Class]; !ok {
		return fmt.Errorf("metric generation hint for %q has unsupported class %q", metricName, h.Class)
	}
	return nil
}

type ScenarioGenerationHints struct {
	MetricGenerationHints map[string]MetricGenerationHint `json:"metric_generation_hints" yaml:"metric_generation_hints"`
}

func (h MetricGenerationHint) AdvanceNumberDataPoint(v pmetric.NumberDataPoint, rand *rand.Rand, metric pmetric.Metric, dist DistributionCfg, opts AdvanceOptions) {
	strategy, ok := strategies[h.Class]
	if !ok {
		panic(fmt.Sprintf("unsupported generation hint class %q", h.Class))
	}
	current := currentNumberDataPointValue(v)
	next := strategy.Advance(current, rand, metric, dist, opts)

	switch v.ValueType() {
	case pmetric.NumberDataPointValueTypeDouble:
		if p, ok := opts.Precision[metric.Name()]; ok {
			next = roundToPrecision(next, p)
		}
		v.SetDoubleValue(next)
	case pmetric.NumberDataPointValueTypeInt:
		v.SetIntValue(int64(math.Max(0, math.Round(next))))
	}
}

type clockStrategy struct{}

func (clockStrategy) Advance(current float64, _ *rand.Rand, _ pmetric.Metric, _ DistributionCfg, opts AdvanceOptions) float64 {
	return current + opts.Interval.Seconds()
}

type constantStrategy struct{}

func (constantStrategy) Advance(current float64, _ *rand.Rand, _ pmetric.Metric, _ DistributionCfg, _ AdvanceOptions) float64 {
	return current
}

type currentCountStrategy struct{}

func (currentCountStrategy) Advance(current float64, rand *rand.Rand, _ pmetric.Metric, _ DistributionCfg, _ AdvanceOptions) float64 {
	step := 0.0
	switch p := rand.Float64(); {
	case p < 0.70:
		step = 0
	case p < 0.90:
		step = signedUnitStep(rand)
	default:
		step = 2 * signedUnitStep(rand)
	}
	return math.Max(0, current+step)
}

type slowGaugeStrategy struct{}

func (slowGaugeStrategy) Advance(current float64, rand *rand.Rand, metric pmetric.Metric, dist DistributionCfg, _ AdvanceOptions) float64 {
	scale := math.Max(math.Abs(current), 1)
	value := math.Abs(current + rand.NormFloat64()*math.Max(dist.StdDev*0.2, scale*0.005))
	if strings.HasSuffix(metric.Name(), ".utilization") {
		value = min(value, 1)
	}
	return value
}

type sparseCounterStrategy struct{}

func (sparseCounterStrategy) Advance(current float64, rand *rand.Rand, metric pmetric.Metric, dist DistributionCfg, _ AdvanceOptions) float64 {
	delta := 0.0
	if rand.Float64() < 0.05 {
		delta = math.Abs(rand.NormFloat64() * dist.StdDev)
		if delta == 0 {
			delta = 1
		}
	}
	return applyCounterDelta(metric, current, delta)
}

type stableBinaryStrategy struct{}

func (stableBinaryStrategy) Advance(current float64, _ *rand.Rand, _ pmetric.Metric, _ DistributionCfg, _ AdvanceOptions) float64 {
	if math.Round(current) < 0.5 {
		return 0
	}
	return 1
}

type steadyCounterStrategy struct{}

func (steadyCounterStrategy) Advance(current float64, rand *rand.Rand, metric pmetric.Metric, dist DistributionCfg, _ AdvanceOptions) float64 {
	delta := math.Abs(rand.NormFloat64()*dist.StdDev + float64(dist.MedianMonotonicSum))
	return applyCounterDelta(metric, current, delta)
}

func currentNumberDataPointValue(v pmetric.NumberDataPoint) float64 {
	switch v.ValueType() {
	case pmetric.NumberDataPointValueTypeDouble:
		return v.DoubleValue()
	case pmetric.NumberDataPointValueTypeInt:
		return float64(v.IntValue())
	default:
		return 0
	}
}

func applyCounterDelta(metric pmetric.Metric, current float64, delta float64) float64 {
	if isDelta(&metric) {
		return delta
	}
	return current + delta
}

func signedUnitStep(rand *rand.Rand) float64 {
	if rand.Float64() < 0.5 {
		return -1
	}
	return 1
}
