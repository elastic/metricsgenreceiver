package resourceattr

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"github.com/google/uuid"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"io"
	"math/rand"
	"os"
	"time"

	"net"
	"text/template"
)

func GetResources(path string, startTime time.Time, scale int, r *rand.Rand) ([]pcommon.Resource, error) {
	resourceTemplate, err := getResourceTemplate(path)
	if err != nil {
		return nil, err
	}

	resources, err := renderResources(resourceTemplate, startTime, scale, r)
	if err != nil {
		return nil, err
	}
	return resources, nil
}

func getResourceTemplate(path string) (pcommon.Resource, error) {
	path += "-resource-attributes.json"
	// load a file from the file system into a buffer. the `path` variable is a string that contains the path to the file. use io.ReadAll
	file, err := os.Open(path)
	if err != nil {
		return pcommon.Resource{}, err
	}

	content, err := io.ReadAll(file)
	if err != nil {
		return pcommon.Resource{}, err
	}
	err = file.Close()
	if err != nil {
		return pcommon.Resource{}, err
	}

	metricsUnmarshaler := &pmetric.JSONUnmarshaler{}
	metrics, err := metricsUnmarshaler.UnmarshalMetrics(content)
	if err != nil {
		return pcommon.Resource{}, err
	}
	metrics.MarkReadOnly()
	return metrics.ResourceMetrics().At(0).Resource(), nil
}

func renderResources(resourceTemplate pcommon.Resource, startTime time.Time, scale int, r *rand.Rand) ([]pcommon.Resource, error) {
	startTimeString := startTime.Format(time.RFC3339)
	resources := make([]pcommon.Resource, scale)
	for i := 0; i < scale; i++ {
		resource := pcommon.NewResource()
		resources[i] = resource
		RenderResourceAttributes(resourceTemplate, resource, i, startTimeString, r)
	}
	return resources, nil
}

type resourceTemplateModel struct {
	InstanceID        int
	InstanceStartTime string

	rand *rand.Rand
}

func (m *resourceTemplateModel) randByte() byte {
	return byte(m.rand.Int())
}

func (t *resourceTemplateModel) RandomIPv4() string {
	return net.IPv4(t.randByte(), t.randByte(), t.randByte(), t.randByte()).String()
}

func (t *resourceTemplateModel) RandomIPv6() string {
	var buf = make([]byte, net.IPv6len)
	t.rand.Read(buf)
	return net.IP(buf).String()
}

func (t *resourceTemplateModel) RandomMAC() string {
	var mac net.HardwareAddr
	// Set the local bit
	mac = append(mac, t.randByte()|2, t.randByte(), t.randByte(), t.randByte(), t.randByte())
	return mac.String()
}

func (t *resourceTemplateModel) UUID() string {
	uid, _ := uuid.NewRandomFromReader(t.rand)
	return uid.String()
}

func (t *resourceTemplateModel) RandomHex(len int) string {
	var buf = make([]byte, len/2)
	t.rand.Read(buf)
	return hex.EncodeToString(buf)
}

func (t *resourceTemplateModel) RandomIntn(n int) int {
	return t.rand.Intn(n)
}

func (t *resourceTemplateModel) RandomFrom(s ...string) string {
	return s[t.rand.Intn(len(s))]
}

func RenderResourceAttributes(resourceTemplate pcommon.Resource, resource pcommon.Resource, id int, startTimeString string, r *rand.Rand) {
	model := &resourceTemplateModel{
		InstanceID:        id,
		InstanceStartTime: startTimeString,
		rand:              r,
	}
	targetAttr := resource.Attributes()
	resourceTemplate.Attributes().Range(func(k string, v pcommon.Value) bool {
		switch v.Type() {
		case pcommon.ValueTypeStr:
			rendered := processResourceAttributeTemplate(k, v.Str(), model)
			targetAttr.PutStr(k, rendered)
		case pcommon.ValueTypeSlice:
			targetSlice := targetAttr.PutEmptySlice(k)
			for j := 0; j < v.Slice().Len(); j++ {
				sv := v.Slice().At(j)
				if sv.Type() == pcommon.ValueTypeStr {
					rendered := processResourceAttributeTemplate(k, sv.Str(), model)
					targetSlice.AppendEmpty().SetStr(rendered)
				} else {
					panic(fmt.Errorf("unhandled resource attribute type %s: %s", k, v.Type()))
				}
			}
		default:
			panic(fmt.Errorf("unhandled resource attribute type %s: %s", k, v.Type()))
		}
		return true
	})
}

func processResourceAttributeTemplate(k, v string, model *resourceTemplateModel) string {
	tmpl, err := template.New(k).Parse(v)
	if err != nil {
		panic(err)
	}
	buf := new(bytes.Buffer)
	err = tmpl.Execute(buf, model)
	if err != nil {
		panic(err)
	}
	s := buf.String()
	if len(s) == 0 {
		panic(fmt.Errorf("resource attribute template %s: '%s' rendered to empty string", k, v))
	}
	return s
}

func OverrideExistingAttributes(source, target pcommon.Resource) {
	targetAttr := target.Attributes()
	source.Attributes().Range(func(k string, v pcommon.Value) bool {
		if _, exists := targetAttr.Get(k); exists {
			targetValue := targetAttr.PutEmpty(k)
			v.CopyTo(targetValue)
		}
		return true
	})
}
