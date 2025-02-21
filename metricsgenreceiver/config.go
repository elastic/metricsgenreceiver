package metricsgenreceiver

import (
	"fmt"
	"go.opentelemetry.io/collector/component"
	"math/rand"
	"time"
)

type Config struct {
	StartTime      time.Time     `mapstructure:"start_time"`
	EndTime        time.Time     `mapstructure:"end_time"`
	Interval       time.Duration `mapstructure:"interval"`
	IntervalJitter bool          `mapstructure:"interval_jitter"`
	RealTime       bool          `mapstructure:"real_time"`
	ExitAfterEnd   bool          `mapstructure:"exit_after_end"`
	Seed           int64         `mapstructure:"seed"`
	Scenarios      []ScenarioCfg `mapstructure:"scenarios"`
}

type ScenarioCfg struct {
	Path         string                 `mapstructure:"path"`
	Scale        int                    `mapstructure:"scale"`
	Churn        int                    `mapstructure:"churn"`
	TemplateVars map[string]interface{} `mapstructure:"template_vars"`
}

func createDefaultConfig() component.Config {
	return &Config{
		Seed:      rand.Int63(),
		Scenarios: make([]ScenarioCfg, 0),
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
