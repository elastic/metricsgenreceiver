package distribution

import (
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
)

// IdentityHash computes a stable hash for (metric name, attribute set). It does not include the
// instance identifier, so callers can precompute it once per template data point and reuse it
// across every instance/interval emission by mixing in the instance ID.
func IdentityHash(metricName string, attrs pcommon.Map) uint64 {
	var b strings.Builder
	b.Grow(64)
	b.WriteString("metric=")
	b.WriteString(metricName)
	appendAttributesKey(&b, attrs)
	return hashStableKey(b.String())
}

// mixInstanceID derives a per-instance hash from a precomputed identity hash using a splitmix64-style
// finalizer. Cheap (a few arithmetic ops) and avalanches well enough for the narrow ranges we map
// into.
func mixInstanceID(identityHash uint64, instanceID int) uint64 {
	h := identityHash ^ uint64(instanceID)*0x9e3779b97f4a7c15
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	return h
}

// hashToUnitInterval maps a uint64 hash into [0, 1) with 53-bit precision (the full mantissa of a
// float64).
func hashToUnitInterval(hash uint64) float64 {
	return float64(hash>>11) * (1.0 / (1 << 53))
}

func hashStableKey(key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return h.Sum64()
}

func appendAttributesKey(b *strings.Builder, attrs pcommon.Map) {
	for _, key := range sortedMapKeys(attrs) {
		value, _ := attrs.Get(key)
		b.WriteString("|attr=")
		b.WriteString(key)
		b.WriteByte(':')
		appendValueKey(b, value)
	}
}

func appendValueKey(b *strings.Builder, value pcommon.Value) {
	switch value.Type() {
	case pcommon.ValueTypeEmpty:
		b.WriteString("empty")
	case pcommon.ValueTypeStr:
		b.WriteString("str:")
		b.WriteString(value.Str())
	case pcommon.ValueTypeInt:
		b.WriteString("int:")
		b.WriteString(strconv.FormatInt(value.Int(), 10))
	case pcommon.ValueTypeDouble:
		b.WriteString("double:")
		b.WriteString(strconv.FormatFloat(value.Double(), 'g', -1, 64))
	case pcommon.ValueTypeBool:
		b.WriteString("bool:")
		b.WriteString(strconv.FormatBool(value.Bool()))
	case pcommon.ValueTypeBytes:
		b.WriteString("bytes:")
		b.WriteString(hex.EncodeToString(value.Bytes().AsRaw()))
	case pcommon.ValueTypeMap:
		b.WriteByte('{')
		nested := value.Map()
		for i, key := range sortedMapKeys(nested) {
			if i > 0 {
				b.WriteByte(',')
			}
			nestedValue, _ := nested.Get(key)
			b.WriteString(key)
			b.WriteByte(':')
			appendValueKey(b, nestedValue)
		}
		b.WriteByte('}')
	case pcommon.ValueTypeSlice:
		b.WriteByte('[')
		values := value.Slice()
		for i := 0; i < values.Len(); i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			appendValueKey(b, values.At(i))
		}
		b.WriteByte(']')
	default:
		panic(fmt.Sprintf("unsupported attribute value type %v", value.Type()))
	}
}

func sortedMapKeys(attrs pcommon.Map) []string {
	keys := make([]string, 0, attrs.Len())
	attrs.Range(func(k string, _ pcommon.Value) bool {
		keys = append(keys, k)
		return true
	})
	sort.Strings(keys)
	return keys
}
