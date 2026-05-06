package metricsgenreceiver

import (
	"fmt"
	"time"

	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/distribution"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

type Config struct {
	StartTime                         time.Time                    `mapstructure:"start_time"`
	StartNowMinus                     time.Duration                `mapstructure:"start_now_minus"`
	EndTime                           time.Time                    `mapstructure:"end_time"`
	EndNowMinus                       time.Duration                `mapstructure:"end_now_minus"`
	Interval                          time.Duration                `mapstructure:"interval"`
	IntervalJitterStdDev              time.Duration                `mapstructure:"interval_jitter_std_dev"`
	RealTime                          bool                         `mapstructure:"real_time"`
	RunIndefinitely                   bool                         `mapstructure:"run_indefinitely"`
	ExitAfterEnd                      bool                         `mapstructure:"exit_after_end"`
	ExitAfterEndTimeout               time.Duration                `mapstructure:"exit_after_end_timeout"`
	Seed                              int64                        `mapstructure:"seed"`
	InstanceOffset                    uint                         `mapstructure:"instance_offset"`
	Scenarios                         []ScenarioCfg                `mapstructure:"scenarios"`
	Distribution                      distribution.DistributionCfg `mapstructure:"distribution"`
	ExponentialHistogramsTemplatePath string                       `mapstructure:"exponential_histograms_template_path"`
}

type ScenarioCfg struct {
	Path                string         `mapstructure:"path"`
	Scale               int            `mapstructure:"scale"`
	Concurrency         int            `mapstructure:"concurrency"`
	Churn               *ChurnCfg      `mapstructure:"churn"`
	TemplateVars        map[string]any `mapstructure:"template_vars"`
	TemporalityOverride string         `mapstructure:"temporality_override"`
	HistogramOverride   string         `mapstructure:"histogram_override"`
}

type ChurnCfg struct {
	InstanceLifetime time.Duration `mapstructure:"instance_lifetime"`
	SamplesPerSeries int           `mapstructure:"samples_per_series"`
}

// churnRate is the number of instance replacements to apply per collection interval.
// It is stored as a fraction so sub-interval replacement rates can accumulate exactly.
type churnRate struct {
	replacements int64
	intervals    int64
}

func (r churnRate) enabled() bool {
	return r.intervals != 0
}

func newChurnRate(churn *ChurnCfg, scale int, interval time.Duration) churnRate {
	if churn == nil {
		return churnRate{}
	}
	if churn.SamplesPerSeries > 0 {
		return reduceChurnRate(int64(scale), int64(churn.SamplesPerSeries))
	}

	return reduceChurnRate(int64(scale)*int64(interval), int64(churn.InstanceLifetime))
}

func reduceChurnRate(replacements, intervals int64) churnRate {
	// Keep the accumulator denominator small without changing the replacement rate.
	divisor := gcdInt64(replacements, intervals)
	return churnRate{
		replacements: replacements / divisor,
		intervals:    intervals / divisor,
	}
}

func gcdInt64(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func (c ScenarioCfg) AggregationTemporalityOverride() pmetric.AggregationTemporality {
	switch c.TemporalityOverride {
	case "cumulative":
		return pmetric.AggregationTemporalityCumulative
	case "delta":
		return pmetric.AggregationTemporalityDelta
	default:
		return pmetric.AggregationTemporalityUnspecified
	}
}

func (c Config) GetExponentialHistogramsTemplatePath() string {
	if c.ExponentialHistogramsTemplatePath != "" {
		return c.ExponentialHistogramsTemplatePath
	}
	return "builtin/exponential-histograms-low-frequency.ndjson"
}

func (c ScenarioCfg) ForceExponentialHistograms() bool {
	return c.HistogramOverride == "exponential"
}

func createDefaultConfig() component.Config {
	return &Config{
		Seed:         0,
		Scenarios:    make([]ScenarioCfg, 0),
		Distribution: distribution.DefaultDistribution,
	}
}

func (cfg *Config) Validate() error {
	if cfg.Interval.Seconds() < 1 {
		return fmt.Errorf("the interval has to be set to at least 1 second (1s)")
	}

	if cfg.StartTime.After(cfg.EndTime) {
		return fmt.Errorf("start_time must be before end_time")
	}

	if cfg.RealTime && cfg.StartNowMinus > 0 {
		return fmt.Errorf("start_now_minus cannot be used with real_time: true (results in permanent timestamp lag)")
	}

	if cfg.RunIndefinitely && (!cfg.EndTime.IsZero() || cfg.EndNowMinus > 0) {
		return fmt.Errorf("run_indefinitely cannot be combined with end_time or end_now_minus")
	}

	for _, scn := range cfg.Scenarios {
		if scn.Concurrency != 0 && scn.Scale%scn.Concurrency != 0 {
			return fmt.Errorf("scale must be a multiple of concurrency")
		}
		if scn.Concurrency < 0 {
			return fmt.Errorf("concurrency must be a positive number")
		}
		if scn.Churn == nil {
			continue
		}
		if scn.Scale <= 0 {
			return fmt.Errorf("scale must be positive when churn is enabled")
		}
		hasInstanceLifetime := scn.Churn.InstanceLifetime != 0
		hasSamplesPerSeries := scn.Churn.SamplesPerSeries != 0
		if hasInstanceLifetime == hasSamplesPerSeries {
			return fmt.Errorf("exactly one of churn.samples_per_series or churn.instance_lifetime must be set")
		}
		if hasSamplesPerSeries && scn.Churn.SamplesPerSeries < 1 {
			return fmt.Errorf("churn.samples_per_series must be greater than or equal to 1")
		}
		if !hasInstanceLifetime {
			continue
		}
		if scn.Churn.InstanceLifetime < 0 {
			return fmt.Errorf("churn.instance_lifetime must be a positive duration")
		}
		if scn.Churn.InstanceLifetime < cfg.Interval {
			return fmt.Errorf("churn.instance_lifetime must be greater than or equal to interval")
		}
	}
	return nil
}
