package metricsgenreceiver

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

	"net"
	"text/template"
)

func getResourceTemplate(scn ScenarioCfg) (pcommon.Resource, error) {
	path := scn.Path
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

func renderResources(resourceTemplate pcommon.Resource, scn ScenarioCfg, r *rand.Rand) ([]pcommon.Resource, error) {
	resources := make([]pcommon.Resource, scn.Scale)
	for i := 0; i < scn.Scale; i++ {
		resource := pcommon.NewResource()
		resources[i] = resource
		err := renderResourceAttributes(resourceTemplate, resource, &resourceTemplateModel{
			InstanceID: i,
			rand:       r,
		})
		if err != nil {
			return nil, err
		}
	}
	return resources, nil
}

type resourceTemplateModel struct {
	InstanceID int

	rand *rand.Rand
}

func (m *resourceTemplateModel) randByte() byte {
	return byte(m.rand.Int())
}

func (t *resourceTemplateModel) RandomIP() string {
	return t.RandomIPv4()
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

func renderResourceAttributes(resourceTemplate pcommon.Resource, resource pcommon.Resource, model *resourceTemplateModel) error {
	attr := resource.Attributes()
	resourceTemplate.CopyTo(resource)
	var outerErr error
	resource.Attributes().Range(func(k string, v pcommon.Value) bool {
		// switch v.type
		switch v.Type() {
		case pcommon.ValueTypeStr:
			rendered, err := processResourceAttributeTemplate(k, v.Str(), model)
			if err != nil {
				outerErr = err
				return false
			}
			attr.PutStr(k, rendered)
		case pcommon.ValueTypeSlice:
			// iterate v.Slice()
			for j := 0; j < v.Slice().Len(); j++ {
				v := v.Slice().At(j)
				if v.Type() == pcommon.ValueTypeStr {
					rendered, err := processResourceAttributeTemplate(k, v.Str(), model)
					if err != nil {
						outerErr = err
						return false
					}
					v.SetStr(rendered)
				} else {
					outerErr = fmt.Errorf("unhandled resource attribute type %s: %s", k, v.Type())
					return false
				}
			}
		default:
			outerErr = fmt.Errorf("unhandled resource attribute type %s: %s", k, v.Type())
			return false
		}
		return true
	})
	return outerErr
}

func processResourceAttributeTemplate(k, v string, model *resourceTemplateModel) (string, error) {
	tmpl, err := template.New(k).Parse(v)
	if err != nil {
		return "", err
	}
	buf := new(bytes.Buffer)
	err = tmpl.Execute(buf, model)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}
