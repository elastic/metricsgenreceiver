package metricstmpl

import (
	"bytes"
	"encoding/hex"
	"github.com/google/uuid"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"math/rand"
	"path/filepath"
	"text/template"
	"time"

	"net"
)

func RenderMetricsTemplate(path string, templateModel any) (pmetric.Metrics, error) {
	funcMap := template.FuncMap{
		"loop": func(from, to int) <-chan int {
			ch := make(chan int)
			go func() {
				for i := from; i <= to; i++ {
					ch <- i
				}
				close(ch)
			}()
			return ch
		},
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return pmetric.Metrics{}, err
	}
	tpl, err := template.New(path).Funcs(funcMap).ParseFiles(path)
	if err != nil {
		return pmetric.Metrics{}, err
	}

	buf := new(bytes.Buffer)
	err = tpl.ExecuteTemplate(buf, filepath.Base(path), templateModel)
	if err != nil {
		return pmetric.Metrics{}, err
	}

	metricsUnmarshaler := &pmetric.JSONUnmarshaler{}
	metrics, err := metricsUnmarshaler.UnmarshalMetrics(buf.Bytes())
	return metrics, err
}

func GetResources(path string, startTime time.Time, scale int, r *rand.Rand) ([]pcommon.Resource, error) {
	startTimeString := startTime.Format(time.RFC3339)
	resources := make([]pcommon.Resource, scale)
	for i := 0; i < scale; i++ {
		resource, err := RenderResource(path, i, startTimeString, r)
		if err != nil {
			return nil, err
		}
		resources[i] = resource
	}
	return resources, nil
}

func RenderResource(path string, id int, startTimeString string, r *rand.Rand) (pcommon.Resource, error) {
	metricsTemplate, err := RenderMetricsTemplate(path+"-resource-attributes.json", &resourceTemplateModel{
		InstanceID:        id,
		InstanceStartTime: startTimeString,
		rand:              r,
	})
	if err != nil {
		return pcommon.Resource{}, err
	}
	return metricsTemplate.ResourceMetrics().At(0).Resource(), nil
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
