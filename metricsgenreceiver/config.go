package metricsgenreceiver

import (
	"fmt"
	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/metricsgenreceiver/internal/distribution"
	"go.opentelemetry.io/collector/component"
	"time"
)

type Config struct {
	StartTime      time.Time                    `mapstructure:"start_time"`
	StartNowMinus  time.Duration                `mapstructure:"start_now_minus"`
	EndTime        time.Time                    `mapstructure:"end_time"`
	EndNowMinus    time.Duration                `mapstructure:"end_now_minus"`
	Interval       time.Duration                `mapstructure:"interval"`
	IntervalJitter bool                         `mapstructure:"interval_jitter"`
	RealTime       bool                         `mapstructure:"real_time"`
	ExitAfterEnd   bool                         `mapstructure:"exit_after_end"`
	Seed           int64                        `mapstructure:"seed"`
	Scenarios      []ScenarioCfg                `mapstructure:"scenarios"`
	Distribution   distribution.DistributionCfg `mapstructure:"distribution"`
}

type ScenarioCfg struct {
	Path                string                 `mapstructure:"path"`
	Scale               int                    `mapstructure:"scale"`
	ConcurrentInstances bool                   `mapstructure:"concurrent_instances"`
	Churn               int                    `mapstructure:"churn"`
	TemplateVars        map[string]interface{} `mapstructure:"template_vars"`
}

func createDefaultConfig() component.Config {
	return &Config{
		Seed:      0,
		Scenarios: make([]ScenarioCfg, 0),
		Distribution: distribution.DistributionCfg{
			MedianMonotonicSum: 100,
			StdDevGaugePct:     0.05,
			StdDev:             5.0,
		},
	}
}

func (cfg *Config) Validate() error {
	if cfg.Interval.Seconds() < 1 {
		return fmt.Errorf("the interval has to be set to at least 1 second (1s)")
	}

	if cfg.StartTime.After(cfg.EndTime) {
		return fmt.Errorf("start_time must be before end_time")
	}
	return nil
}
