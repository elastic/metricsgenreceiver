package metricsgenreceiver

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/distribution"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/confmap/confmaptest"
	"go.opentelemetry.io/collector/confmap/xconfmap"
)

func TestLoadConfig(t *testing.T) {
	cm, err := confmaptest.LoadConf(filepath.Join("testdata", "config.yaml"))
	require.NoError(t, err)
	factory := NewFactory()
	cfg := factory.CreateDefaultConfig().(*Config)
	sub, err := cm.Sub("metricsgen")
	require.NoError(t, err)
	require.NoError(t, sub.Unmarshal(cfg))

	assert.NoError(t, xconfmap.Validate(cfg))
	assert.Equal(t, testdataConfigYamlAsMap(), cfg)
}

func testdataConfigYamlAsMap() *Config {
	startTime, _ := time.Parse(time.RFC3339, "2024-12-17T00:00:00Z")
	endTime, _ := time.Parse(time.RFC3339, "2024-12-17T00:00:31Z")
	interval, _ := time.ParseDuration("30s")
	return &Config{
		StartTime: startTime,
		EndTime:   endTime,
		Interval:  interval,
		Seed:      123,
		Scenarios: []ScenarioCfg{
			{
				Path:  "testdata/metricstemplate",
				Scale: 10,
			},
		},
		Distribution: distribution.DistributionCfg{
			MedianMonotonicSum: 100,
			StdDevGaugePct:     0.01,
			StdDev:             1.0,
		},
	}
}

func TestConfig_ExponentialHistogramsTemplatePath(t *testing.T) {
	t.Run("custom path", func(t *testing.T) {
		cfg := &Config{
			ExponentialHistogramsTemplatePath: "custom/path/histograms.ndjson",
		}
		assert.Equal(t, "custom/path/histograms.ndjson", cfg.GetExponentialHistogramsTemplatePath())
	})

	t.Run("default path when empty", func(t *testing.T) {
		cfg := &Config{
			ExponentialHistogramsTemplatePath: "",
		}
		assert.Equal(t, "builtin/exponential-histograms-low-frequency.ndjson", cfg.GetExponentialHistogramsTemplatePath())
	})

	t.Run("default path when not set", func(t *testing.T) {
		cfg := &Config{}
		assert.Equal(t, "builtin/exponential-histograms-low-frequency.ndjson", cfg.GetExponentialHistogramsTemplatePath())
	})
}

func TestScenarioCfg_ForceExponentialHistograms(t *testing.T) {
	t.Run("histogram_override set to exponential", func(t *testing.T) {
		scenario := ScenarioCfg{
			HistogramOverride: "exponential",
		}
		assert.True(t, scenario.ForceExponentialHistograms())
	})

	t.Run("histogram_override set to other value", func(t *testing.T) {
		scenario := ScenarioCfg{
			HistogramOverride: "normal",
		}
		assert.False(t, scenario.ForceExponentialHistograms())
	})

	t.Run("histogram_override empty", func(t *testing.T) {
		scenario := ScenarioCfg{
			HistogramOverride: "",
		}
		assert.False(t, scenario.ForceExponentialHistograms())
	})

	t.Run("histogram_override not set", func(t *testing.T) {
		scenario := ScenarioCfg{}
		assert.False(t, scenario.ForceExponentialHistograms())
	})
}
