package metricsgenreceiver

import (
	"fmt"
	"go.opentelemetry.io/collector/component"
	"math/rand"
	"time"
)

type Config struct {
	Path               string                 `mapstructure:"path"`
	StartTime          time.Time              `mapstructure:"start_time"`
	EndTime            time.Time              `mapstructure:"end_time"`
	Interval           time.Duration          `mapstructure:"interval"`
	RealTime           bool                   `mapstructure:"real_time"`
	ExitAfterEnd       bool                   `mapstructure:"exit_after_end"`
	Seed               int64                  `mapstructure:"seed"`
	Scale              int                    `mapstructure:"scale"`
	Churn              int                    `mapstructure:"churn"`
	ResourceAttributes map[string]interface{} `mapstructure:"resource_attributes"`
	TemplateVars       map[string]interface{} `mapstructure:"template_vars"`
}

func createDefaultConfig() component.Config {
	return &Config{
		Seed:  rand.Int63(),
		Scale: 1,
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
