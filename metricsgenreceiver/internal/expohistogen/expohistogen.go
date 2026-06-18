package expohistogen

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"strings"

	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/dp"
	"github.com/lightstep/go-expohisto/structure"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

//go:embed builtin
var fsys embed.FS

// Generator generates random exponential histograms from a set of pre-loaded samples.
type Generator struct {
	samples []pmetric.ExponentialHistogramDataPoint
}

// NewGenerator creates a new Generator by loading exponential histogram samples from the given file path.
// If the path starts with "builtin/", it loads from the embedded file system.
// Otherwise, it loads from the file system.
func NewGenerator(path string) (*Generator, error) {
	samples, err := loadExponentialHistogramsFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load exponential histograms: %w", err)
	}

	if len(samples) == 0 {
		return nil, fmt.Errorf("no exponential histogram samples with delta temporality found in %s", path)
	}

	return &Generator{
		samples: samples,
	}, nil
}

// Generate returns a random exponential histogram data point from the loaded samples.
func (g *Generator) GenerateInto(r *rand.Rand, target pmetric.ExponentialHistogramDataPoint) {
	// Select a random sample
	idx := r.Intn(len(g.samples))
	sample := g.samples[idx]

	// Create a copy to avoid modifying the original sample
	posBuckets := randomizeBuckets(r, sample.Positive())
	posSummary := randomizedBucketSummary(r, posBuckets, sample.Scale())
	negBuckets := randomizeBuckets(r, sample.Negative())
	negSummary := randomizedBucketSummary(r, negBuckets, sample.Scale())

	target.SetScale(sample.Scale())
	target.SetZeroCount(sample.ZeroCount())
	target.SetZeroThreshold(sample.ZeroThreshold())
	target.RemoveMin()
	target.RemoveMax()
	target.RemoveSum()

	target.SetCount(posSummary.Count + negSummary.Count)
	target.SetSum(posSummary.Sum - negSummary.Sum)

	if negSummary.Count > 0 {
		target.SetMin(-negSummary.Max)
	} else if posSummary.Count > 0 {
		target.SetMin(posSummary.Min)
	}
	if posSummary.Count > 0 {
		target.SetMax(posSummary.Max)
	} else if negSummary.Count > 0 {
		target.SetMax(-negSummary.Min)
	}

	// Set the randomized buckets
	posBuckets.CopyTo(target.Positive())
	negBuckets.CopyTo(target.Negative())
}

const cumulativeMaxSize = 320

// MergeInto generates a random delta and merges it into the target
// data point's existing accumulated value. The target is converted to a
// go-expohisto structure, the delta is merged in, and the result is written back.
// Min, max, and sum are tracked from the original pdata values rather than the
// synthetic midpoints used for bucket replay.
func (g *Generator) MergeInto(r *rand.Rand, target pmetric.ExponentialHistogramDataPoint) {
	delta := pmetric.NewExponentialHistogramDataPoint()
	g.GenerateInto(r, delta)

	prevSum := target.Sum()
	prevMin := target.Min()
	prevMax := target.Max()
	prevCount := target.Count()

	acc := pdataToAccumulator(target)
	mergePdataIntoAccumulator(acc, delta)
	writeAccumulatorToPdata(acc, target)

	newSum := prevSum + delta.Sum()
	target.SetSum(newSum)

	if prevCount == 0 {
		if delta.HasMin() {
			target.SetMin(delta.Min())
		}
		if delta.HasMax() {
			target.SetMax(delta.Max())
		}
	} else {
		newMin := prevMin
		if delta.HasMin() && delta.Min() < newMin {
			newMin = delta.Min()
		}
		target.SetMin(newMin)

		newMax := prevMax
		if delta.HasMax() && delta.Max() > newMax {
			newMax = delta.Max()
		}
		target.SetMax(newMax)
	}
}

func pdataToAccumulator(dp pmetric.ExponentialHistogramDataPoint) *structure.Float64 {
	acc := &structure.Float64{}
	acc.Init(structure.NewConfig(structure.WithMaxSize(int32(cumulativeMaxSize))))
	mergePdataIntoAccumulator(acc, dp)
	return acc
}

// mergePdataIntoAccumulator replays the buckets of a pdata delta data point into
// the go-expohisto accumulator via UpdateByIncr.
func mergePdataIntoAccumulator(acc *structure.Float64, delta pmetric.ExponentialHistogramDataPoint) {
	scale := delta.Scale()

	posBuckets := delta.Positive()
	for i := 0; i < posBuckets.BucketCounts().Len(); i++ {
		count := posBuckets.BucketCounts().At(i)
		if count > 0 {
			bucketIdx := int(posBuckets.Offset()) + i
			acc.UpdateByIncr(bucketMidpoint(bucketIdx, int(scale)), count)
		}
	}

	negBuckets := delta.Negative()
	for i := 0; i < negBuckets.BucketCounts().Len(); i++ {
		count := negBuckets.BucketCounts().At(i)
		if count > 0 {
			bucketIdx := int(negBuckets.Offset()) + i
			acc.UpdateByIncr(-bucketMidpoint(bucketIdx, int(scale)), count)
		}
	}

	if delta.ZeroCount() > 0 {
		acc.UpdateByIncr(0, delta.ZeroCount())
	}
}

// bucketMidpoint returns a representative value for exponential histogram bucket
// at the given index and scale. The value is chosen so that go-expohisto's
// MapToIndex maps it back to the same bucket index.
func bucketMidpoint(index int, scale int) float64 {
	return math.Exp2((float64(index) + 0.5) * math.Ldexp(1.0, -scale))
}

// writeAccumulatorToPdata writes the go-expohisto histogram state into a pdata
// ExponentialHistogramDataPoint, preserving attributes and timestamps.
func writeAccumulatorToPdata(h *structure.Float64, target pmetric.ExponentialHistogramDataPoint) {
	target.SetScale(h.Scale())
	target.SetCount(h.Count())
	target.SetSum(h.Sum())
	target.SetZeroCount(h.ZeroCount())
	target.SetZeroThreshold(0)

	if h.Count() > 0 {
		target.SetMin(h.Min())
		target.SetMax(h.Max())
	} else {
		target.RemoveMin()
		target.RemoveMax()
	}

	writeBucketsToPdata(h.Positive(), target.Positive())
	writeBucketsToPdata(h.Negative(), target.Negative())
}

func writeBucketsToPdata(src *structure.Buckets, target pmetric.ExponentialHistogramDataPointBuckets) {
	fresh := pmetric.NewExponentialHistogramDataPointBuckets()
	fresh.SetOffset(src.Offset())
	for i := uint32(0); i < src.Len(); i++ {
		fresh.BucketCounts().Append(src.At(i))
	}
	fresh.CopyTo(target)
}

type bucketSummary struct {
	Count uint64
	Sum   float64
	Min   float64
	Max   float64
}

func randomizedBucketSummary(r *rand.Rand, buckets pmetric.ExponentialHistogramDataPointBuckets, scale int32) bucketSummary {
	summary := bucketSummary{
		Count: 0,
		Min:   math.MaxFloat64,
		Max:   -math.MaxFloat64,
		Sum:   0,
	}
	for i := 0; i < buckets.BucketCounts().Len(); i++ {
		count := buckets.BucketCounts().At(i)
		if count > 0 {
			value := LowerBucketBoundary(float64(buckets.Offset()+int32(i))+r.Float64(), int(scale))
			summary.Count += count
			summary.Sum += value * float64(count)
			if value < summary.Min {
				summary.Min = value
			}
			if value > summary.Max {
				summary.Max = value
			}
		}
	}
	return summary
}

func LowerBucketBoundary(index float64, scale int) float64 {
	inverseFactor := math.Ldexp(math.Ln2, -scale)
	return 2.0 * math.Exp((index-float64(int64(1)<<scale))*inverseFactor)
}

// Randomized the provided buckets. Will never return empty buckets if the input is non-empty.
func randomizeBuckets(r *rand.Rand, buckets pmetric.ExponentialHistogramDataPointBuckets) pmetric.ExponentialHistogramDataPointBuckets {

	// check if buckets has at least one non empty bucket
	nonEmptyBuckets := buckets
	hasNonEmpty := false
	for i := 0; i < nonEmptyBuckets.BucketCounts().Len(); i++ {
		if nonEmptyBuckets.BucketCounts().At(i) > 0 {
			hasNonEmpty = true
			break
		}
	}
	if !hasNonEmpty {
		// return as is
		return buckets
	}

	//loop until we generated at least one bucket
	for {
		// Create an array with 2 extra slots (1 before and 1 after)
		originalLen := nonEmptyBuckets.BucketCounts().Len()
		newBuckets := make([]uint64, originalLen+2)
		// Copy original counts into new buckets, with a small probability of shifting them around
		// and adjusting the count from 40% to 160%

		firstPopulatedIndex := len(newBuckets)
		lastPopulatedIndex := -1
		for i := 0; i < originalLen; i++ {
			oldCount := nonEmptyBuckets.BucketCounts().At(i)
			if oldCount > 0 {
				countScale := 0.4 + r.Float64()*1.2
				newCount := uint64(math.Round(float64(oldCount) * countScale))
				if newCount > 0 {
					//40% chance of moving the bucket one to the left or right
					shiftChance := r.Float64()
					offset := 0
					if shiftChance < 0.2 {
						offset = -1
					} else if shiftChance < 0.4 {
						offset = 1
					}
					index := i + 1 + offset
					firstPopulatedIndex = min(firstPopulatedIndex, index)
					lastPopulatedIndex = max(lastPopulatedIndex, index)
					newBuckets[index] += newCount
				}
			}
		}

		// now create the resulting buckets, trimming empty ones at the start and end
		result := pmetric.NewExponentialHistogramDataPointBuckets()
		if lastPopulatedIndex >= firstPopulatedIndex {
			// at least one populated bucket, we have a result
			result.SetOffset(nonEmptyBuckets.Offset() + int32(firstPopulatedIndex) - 1)
			for i := firstPopulatedIndex; i <= lastPopulatedIndex; i++ {
				result.BucketCounts().Append(newBuckets[i])
			}
			return result
		}
	}

}

// loadExponentialHistogramsFromFile loads an NDJSON file where each line is OTLP JSON format
// and extracts all exponential histogram data points with delta temporality from it.
// If the path starts with "builtin/", it loads from the embedded file system.
// Otherwise, it loads from the file system.
func loadExponentialHistogramsFromFile(path string) ([]pmetric.ExponentialHistogramDataPoint, error) {
	var data []byte
	var err error

	if strings.HasPrefix(path, "builtin/") {
		// Load from embedded file system
		data, err = fsys.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read embedded file %s: %w", path, err)
		}
	} else {
		// Load from disk
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read file %s: %w", path, err)
		}
	}

	return loadExponentialHistogramsFromBytes(data)
}

// loadExponentialHistogramsFromBytes loads NDJSON data where each line is OTLP JSON format
// and extracts all exponential histogram data points from it.
func loadExponentialHistogramsFromBytes(data []byte) ([]pmetric.ExponentialHistogramDataPoint, error) {
	var allDataPoints []pmetric.ExponentialHistogramDataPoint
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		// Parse the OTLP JSON line
		var otlpData map[string]interface{}
		if err := json.Unmarshal(line, &otlpData); err != nil {
			return nil, fmt.Errorf("failed to parse JSON at line %d: %w", lineNum, err)
		}

		// Extract exponential histograms from the OTLP structure
		dataPoints, err := extractExponentialHistograms(line)
		if err != nil {
			return nil, fmt.Errorf("failed to extract histograms at line %d: %w", lineNum, err)
		}

		allDataPoints = append(allDataPoints, dataPoints...)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading data: %w", err)
	}

	return allDataPoints, nil
}

// extractExponentialHistograms extracts exponential histogram data points from OTLP JSON bytes
func extractExponentialHistograms(jsonBytes []byte) ([]pmetric.ExponentialHistogramDataPoint, error) {
	// Use pmetric's unmarshaler to parse OTLP JSON
	unmarshaler := &pmetric.JSONUnmarshaler{}
	metrics, err := unmarshaler.UnmarshalMetrics(jsonBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal OTLP metrics: %w", err)
	}

	var dataPoints []pmetric.ExponentialHistogramDataPoint

	// Use ForEachDataPoint to iterate through all data points
	dp.ForEachDataPoint(&metrics, func(_ int, _ pcommon.Resource, _ pcommon.InstrumentationScope, m pmetric.Metric, dataPoint dp.DataPoint) {
		if m.Type() == pmetric.MetricTypeExponentialHistogram && m.ExponentialHistogram().AggregationTemporality() == pmetric.AggregationTemporalityDelta {
			// Type assert to get the exponential histogram data point
			if expHistDP, ok := dataPoint.(pmetric.ExponentialHistogramDataPoint); ok {
				// Create a copy of the data point to avoid reference issues
				dpCopy := pmetric.NewExponentialHistogramDataPoint()
				expHistDP.CopyTo(dpCopy)
				dataPoints = append(dataPoints, dpCopy)
			}
		}
	})

	return dataPoints, nil
}
