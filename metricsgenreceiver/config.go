package metricsgenreceiver

import (
	"fmt"
	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/distribution"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"time"
)

type Config struct {
	StartTime            time.Time                    `mapstructure:"start_time"`
	StartNowMinus        time.Duration                `mapstructure:"start_now_minus"`
	EndTime              time.Time                    `mapstructure:"end_time"`
	EndNowMinus          time.Duration                `mapstructure:"end_now_minus"`
	Interval             time.Duration                `mapstructure:"interval"`
	IntervalJitterStdDev time.Duration                `mapstructure:"interval_jitter_std_dev"`
	RealTime             bool                         `mapstructure:"real_time"`
	ExitAfterEnd         bool                         `mapstructure:"exit_after_end"`
	ExitAfterEndTimeout  time.Duration                `mapstructure:"exit_after_end_timeout"`
	Seed                 int64                        `mapstructure:"seed"`
	Scenarios            []ScenarioCfg                `mapstructure:"scenarios"`
	Distribution         distribution.DistributionCfg `mapstructure:"distribution"`
}

type ScenarioCfg struct {
	Path                string         `mapstructure:"path"`
	Scale               int            `mapstructure:"scale"`
	Concurrency         int            `mapstructure:"concurrency"`
	Churn               int            `mapstructure:"churn"`
	TemplateVars        map[string]any `mapstructure:"template_vars"`
	TemporalityOverride string         `mapstructure:"temporality_override"`
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

	for _, scn := range cfg.Scenarios {
		if scn.Concurrency != 0 && scn.Scale%scn.Concurrency != 0 {
			return fmt.Errorf("scale must be a multiple of concurrency")
		}
		if scn.Concurrency < 0 {
			return fmt.Errorf("concurrency must be a positive number")
		}
	}
	return nil
}
