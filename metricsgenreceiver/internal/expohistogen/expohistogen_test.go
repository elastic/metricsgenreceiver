package expohistogen

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func TestLoadExponentialHistogramsFromFile_HighFrequency(t *testing.T) {
	dataPoints, err := loadExponentialHistogramsFromFile("builtin/exponential-histograms-high-frequency.ndjson")
	require.NoError(t, err)
	require.NotEmpty(t, dataPoints)

	// Verify the number of histograms loaded
	assert.Equal(t, 4320, len(dataPoints), "number of histograms")

	// Verify first data point
	first := dataPoints[0]
	assert.Equal(t, uint64(664), first.Count(), "first histogram count")
	assert.Equal(t, int32(4), first.Scale(), "first histogram scale")
	assert.Equal(t, 476.11773000000034, first.Sum(), "first histogram sum")
	assert.Equal(t, 104, first.Positive().BucketCounts().Len(), "first histogram bucket count")

	// Verify last data point
	last := dataPoints[len(dataPoints)-1]
	assert.Equal(t, uint64(594), last.Count(), "last histogram count")
	assert.Equal(t, int32(4), last.Scale(), "last histogram scale")
	assert.Equal(t, 455.76597999999984, last.Sum(), "last histogram sum")
	assert.Equal(t, 105, last.Positive().BucketCounts().Len(), "last histogram bucket count")
}

func TestLoadExponentialHistogramsFromFile_LowFrequency(t *testing.T) {
	dataPoints, err := loadExponentialHistogramsFromFile("builtin/exponential-histograms-low-frequency.ndjson")
	require.NoError(t, err)
	require.NotEmpty(t, dataPoints)

	// Verify the number of histograms loaded
	assert.Equal(t, 4316, len(dataPoints), "number of histograms")

	// Verify first data point
	first := dataPoints[0]
	assert.Equal(t, uint64(6), first.Count(), "first histogram count")
	assert.Equal(t, int32(5), first.Scale(), "first histogram scale")
	assert.Equal(t, 7.12298, first.Sum(), "first histogram sum")
	assert.Equal(t, 100, first.Positive().BucketCounts().Len(), "first histogram bucket count")

	// Verify last data point
	last := dataPoints[len(dataPoints)-1]
	assert.Equal(t, uint64(9), last.Count(), "last histogram count")
	assert.Equal(t, int32(5), last.Scale(), "last histogram scale")
	assert.Equal(t, 7.0433, last.Sum(), "last histogram sum")
	assert.Equal(t, 120, last.Positive().BucketCounts().Len(), "last histogram bucket count")
}

func TestGenerator_Generate_BasicValidation(t *testing.T) {
	gen, err := NewGenerator("builtin/exponential-histograms-high-frequency.ndjson")
	require.NoError(t, err)

	r := rand.New(rand.NewSource(12345))

	// Generate a histogram and verify basic properties
	dp := pmetric.NewExponentialHistogramDataPoint()
	gen.GenerateInto(r, dp)

	// Verify the histogram is valid
	assert.Greater(t, dp.Count(), uint64(0), "histogram should have count > 0")
	assert.NotEqual(t, int32(0), dp.Scale(), "histogram should have a scale")
	assert.Greater(t, dp.Positive().BucketCounts().Len(), 0, "histogram should have positive buckets")

	// Verify min/max are set when there are values
	if dp.Count() > 0 {
		assert.True(t, dp.HasMin(), "histogram should have min when count > 0")
		assert.True(t, dp.HasMax(), "histogram should have max when count > 0")
		assert.Greater(t, dp.Max(), dp.Min(), "max should be greater than min")
	}
}

func TestGenerator_Generate_BucketCountMatchesTotalCount(t *testing.T) {
	gen, err := NewGenerator("builtin/exponential-histograms-high-frequency.ndjson")
	require.NoError(t, err)

	r := rand.New(rand.NewSource(42))

	// Generate multiple histograms and verify bucket counts sum to total count
	for i := 0; i < 10; i++ {
		dp := pmetric.NewExponentialHistogramDataPoint()
		gen.GenerateInto(r, dp)

		// Sum up all positive bucket counts
		var posSum uint64
		for j := 0; j < dp.Positive().BucketCounts().Len(); j++ {
			posSum += dp.Positive().BucketCounts().At(j)
		}

		// Sum up all negative bucket counts
		var negSum uint64
		for j := 0; j < dp.Negative().BucketCounts().Len(); j++ {
			negSum += dp.Negative().BucketCounts().At(j)
		}

		totalBucketCount := posSum + negSum
		assert.Equal(t, dp.Count(), totalBucketCount,
			"total count should equal sum of bucket counts at iteration %d", i)
	}
}

func TestGenerator_Generate_ProducesDiverseHistograms(t *testing.T) {
	gen, err := NewGenerator("builtin/exponential-histograms-high-frequency.ndjson")
	require.NoError(t, err)

	r := rand.New(rand.NewSource(99))

	// Generate multiple histograms and track diversity
	seenCounts := make(map[uint64]bool)
	seenSums := make(map[float64]bool)

	for i := 0; i < 100; i++ {
		dp := pmetric.NewExponentialHistogramDataPoint()
		gen.GenerateInto(r, dp)
		seenCounts[dp.Count()] = true
		seenSums[dp.Sum()] = true
	}

	// With randomization, we should see many different counts and sums
	assert.Greater(t, len(seenCounts), 50, "should generate diverse counts")
	assert.Greater(t, len(seenSums), 50, "should generate diverse sums")
}

func TestGenerator_Generate_Reproducibility(t *testing.T) {
	gen, err := NewGenerator("builtin/exponential-histograms-high-frequency.ndjson")
	require.NoError(t, err)

	r1 := rand.New(rand.NewSource(777))
	r2 := rand.New(rand.NewSource(777))

	// Generate histograms with the same seed
	for i := 0; i < 10; i++ {
		dp1 := pmetric.NewExponentialHistogramDataPoint()
		gen.GenerateInto(r1, dp1)
		dp2 := pmetric.NewExponentialHistogramDataPoint()
		gen.GenerateInto(r2, dp2)

		assert.Equal(t, dp1.Count(), dp2.Count(), "same seed should produce same count at iteration %d", i)
		assert.Equal(t, dp1.Scale(), dp2.Scale(), "same seed should produce same scale at iteration %d", i)
		assert.Equal(t, dp1.Sum(), dp2.Sum(), "same seed should produce same sum at iteration %d", i)
		assert.Equal(t, dp1.Min(), dp2.Min(), "same seed should produce same min at iteration %d", i)
		assert.Equal(t, dp1.Max(), dp2.Max(), "same seed should produce same max at iteration %d", i)
	}
}

func TestGenerator_Generate_DoesNotModifyOriginalSamples(t *testing.T) {
	gen, err := NewGenerator("builtin/exponential-histograms-high-frequency.ndjson")
	require.NoError(t, err)

	r := rand.New(rand.NewSource(888))

	// Get the first original sample's count
	originalCount := gen.samples[0].Count()

	// Generate a histogram and modify it
	dp1 := pmetric.NewExponentialHistogramDataPoint()
	gen.GenerateInto(r, dp1)
	dp1.SetCount(99999)

	// Verify the original sample wasn't modified
	assert.Equal(t, originalCount, gen.samples[0].Count(),
		"modifying generated histogram should not affect original samples")
}

func TestGenerator_Generate_RandomizationVariesFromOriginal(t *testing.T) {
	gen, err := NewGenerator("builtin/exponential-histograms-high-frequency.ndjson")
	require.NoError(t, err)

	r := rand.New(rand.NewSource(555))

	// Generate many histograms and check that at least some differ from originals
	differentCount := 0
	for i := 0; i < 50; i++ {
		dp := pmetric.NewExponentialHistogramDataPoint()
		gen.GenerateInto(r, dp)

		// Check if this differs from any original sample
		isDifferent := true
		for j := 0; j < len(gen.samples); j++ {
			if dp.Count() == gen.samples[j].Count() && dp.Sum() == gen.samples[j].Sum() {
				isDifferent = false
				break
			}
		}

		if isDifferent {
			differentCount++
		}
	}

	// Most generated histograms should differ from originals due to randomization
	assert.Greater(t, differentCount, 40, "most generated histograms should differ from original samples")
}

func TestGenerator_Generate_SumMatchesBucketDistribution(t *testing.T) {
	gen, err := NewGenerator("builtin/exponential-histograms-high-frequency.ndjson")
	require.NoError(t, err)

	r := rand.New(rand.NewSource(333))

	// Generate a histogram
	dp := pmetric.NewExponentialHistogramDataPoint()
	gen.GenerateInto(r, dp)

	// The sum should be positive if we have positive buckets
	if dp.Positive().BucketCounts().Len() > 0 {
		hasPositiveCounts := false
		for i := 0; i < dp.Positive().BucketCounts().Len(); i++ {
			if dp.Positive().BucketCounts().At(i) > 0 {
				hasPositiveCounts = true
				break
			}
		}
		if hasPositiveCounts {
			assert.Greater(t, dp.Sum(), float64(0), "sum should be positive when positive buckets exist")
		}
	}
}

func TestGenerator_Generate_NeverGeneratesEmptyHistogram(t *testing.T) {
	gen, err := NewGenerator("builtin/exponential-histograms-high-frequency.ndjson")
	require.NoError(t, err)

	r := rand.New(rand.NewSource(111))

	// Generate many histograms and verify none are empty
	for i := 0; i < 100; i++ {
		dp := pmetric.NewExponentialHistogramDataPoint()
		gen.GenerateInto(r, dp)
		assert.Greater(t, dp.Count(), uint64(0), "histogram %d should never be empty", i)

		// At least one bucket should have counts
		totalBuckets := uint64(0)
		for j := 0; j < dp.Positive().BucketCounts().Len(); j++ {
			totalBuckets += dp.Positive().BucketCounts().At(j)
		}
		for j := 0; j < dp.Negative().BucketCounts().Len(); j++ {
			totalBuckets += dp.Negative().BucketCounts().At(j)
		}
		assert.Greater(t, totalBuckets, uint64(0), "histogram %d should have at least one non-empty bucket", i)
	}
}
