package metricsgenreceiver

import "go.opentelemetry.io/collector/receiver"

// waitForCompletion blocks until the receiver's generation goroutine exits.
// Used by benchmarks to time only the generation phase, not setup/teardown.
func waitForCompletion(r receiver.Metrics) {
	r.(*MetricsGenReceiver).wg.Wait()
}
