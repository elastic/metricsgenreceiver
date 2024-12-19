package metricsgenreceiver

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver/receivertest"
	"testing"
	"time"
)

func TestReceiver(t *testing.T) {
	sink := new(consumertest.MetricsSink)

	factory := NewFactory()
	cfg := testdataConfigYamlAsMap()
	rcv, err := factory.CreateMetrics(context.Background(), receivertest.NewNopSettings(), cfg, sink)
	require.NoError(t, err)
	err = rcv.Start(context.Background(), nil)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return sink.DataPointCount() == 3* // 3 metrics
			2* // 2 intervals
			cfg.Scale
	}, 2*time.Second, time.Millisecond)
	require.NoError(t, rcv.Shutdown(context.Background()))

	allMetrics := sink.AllMetrics()
	require.NotEmpty(t, allMetrics)

	require.Equal(t, 2*cfg.Scale, len(allMetrics))

	verifyMetrics(t, 0, cfg, allMetrics, cfg.StartTime)
	verifyMetrics(t, cfg.Scale, cfg, allMetrics, cfg.StartTime.Add(30*time.Second))
}

func verifyMetrics(t *testing.T, offset int, cfg *Config, allMetrics []pmetric.Metrics, timestamp time.Time) {
	for i := offset; i < cfg.Scale+offset; i++ {
		forEachDataPoint(&allMetrics[i], func(r pcommon.Resource, s pcommon.InstrumentationScope, m pmetric.Metric, dp dataPoint) {
			value, _ := r.Attributes().Get("host.name")
			require.Equal(t, fmt.Sprintf("host-%d", i-offset), value.Str())
			require.Equal(t, cfg.StartTime, dp.StartTimestamp().AsTime())
			require.Equal(t, timestamp, dp.Timestamp().AsTime())
		})
	}
}
