package metricstmpl

import (
	"errors"
	"fmt"

	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/distribution"
	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/dp"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

// GenerationHintMetadataKey is the per-metric metadata key used to declare a generation hint
// inline in the template. Values are the string form of a distribution.GenerationHintClass
// (e.g. "steady_counter"). The receiver reads and validates this key at startup, removes it
// from the template so it does not leak into emitted data, and falls back to default evolution
// for any metric that does not declare one.
const GenerationHintMetadataKey = "metricsgen.hint.class"

// ExtractGenerationHints reads GenerationHintMetadataKey from each metric's metadata, validates
// the class, strips the key so it does not propagate to emitted data, and returns a
// metric-name → hint map. If any metric declares an invalid class, all validation errors are
// joined and returned; the template is not mutated in that case.
func ExtractGenerationHints(metrics *pmetric.Metrics) (map[string]distribution.MetricGenerationHint, error) {
	hints := make(map[string]distribution.MetricGenerationHint)
	var errs []error
	dp.ForEachMetric(metrics, func(_ pcommon.Resource, _ pcommon.InstrumentationScope, m pmetric.Metric) {
		v, ok := m.Metadata().Get(GenerationHintMetadataKey)
		if !ok {
			return
		}
		if v.Type() != pcommon.ValueTypeStr {
			errs = append(errs, fmt.Errorf("metric %q has non-string %s metadata", m.Name(), GenerationHintMetadataKey))
			return
		}
		hint := distribution.MetricGenerationHint{Class: distribution.GenerationHintClass(v.Str())}
		if err := hint.Validate(m.Name()); err != nil {
			errs = append(errs, err)
			return
		}
		hints[m.Name()] = hint
	})
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	// Only strip on success, so the template stays inspectable when reporting validation errors.
	dp.ForEachMetric(metrics, func(_ pcommon.Resource, _ pcommon.InstrumentationScope, m pmetric.Metric) {
		m.Metadata().Remove(GenerationHintMetadataKey)
	})
	return hints, nil
}
