package metricsgenreceiver

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestExitCoordinatorLastFinisherOnly(t *testing.T) {
	tests := []struct {
		name      string
		receivers int
	}{
		{name: "single receiver", receivers: 1},
		{name: "two receivers", receivers: 2},
		{name: "many receivers", receivers: 16},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c exitCoordinator
			for range tt.receivers {
				c.register()
			}

			var last atomic.Int32
			var wg sync.WaitGroup
			for range tt.receivers {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if c.finish() {
						last.Add(1)
					}
				}()
			}
			wg.Wait()

			if got := last.Load(); got != 1 {
				t.Fatalf("exactly one receiver must be the last to finish, got %d", got)
			}
			if c.finish() {
				t.Fatal("finish after all receivers finished must not report another last finisher")
			}
		})
	}
}
