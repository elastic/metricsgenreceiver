package metricstmpl

import (
	"bytes"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"gopkg.in/yaml.v3"
)

//go:embed builtin
var fsys embed.FS

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

	for _, ext := range []string{".json", ".yaml", ".yml"} {
		p := path + ext
		var tpl *template.Template
		var err error
		if strings.HasPrefix(p, "builtin/") {
			tpl, err = template.New(p).Funcs(funcMap).ParseFS(fsys, p)
		} else {
			tpl, err = template.New(p).Funcs(funcMap).ParseFiles(p)
		}
		if err != nil {
			continue
		}

		buf := new(bytes.Buffer)
		err = tpl.ExecuteTemplate(buf, filepath.Base(p), templateModel)
		if err != nil {
			return pmetric.Metrics{}, err
		}
		b := buf.Bytes()
		if strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml") {
			var m map[string]any
			if err = yaml.Unmarshal(b, &m); err != nil {
				return pmetric.Metrics{}, err
			}
			b, err = json.Marshal(m)
			if err != nil {
				return pmetric.Metrics{}, err
			}
		}
		metricsUnmarshaler := &pmetric.JSONUnmarshaler{}
		metrics, err := metricsUnmarshaler.UnmarshalMetrics(b)
		return metrics, err
	}
	return pmetric.Metrics{}, fmt.Errorf("no .json/.yaml/.yml template file found for %s", path)
}

func GetResources(path string, startTime time.Time, scale int, vars map[string]any, r *rand.Rand, seed int64, seedRandomizerEnabled bool) ([]pcommon.Resource, error) {
	startTimeString := startTime.Format(time.RFC3339)
	resources := make([]pcommon.Resource, scale)
	for i := 0; i < scale; i++ {
		resource, err := RenderResource(path, i, startTimeString, vars, r, seed, seedRandomizerEnabled)
		if err != nil {
			return nil, err
		}
		resources[i] = resource
	}
	return resources, nil
}

func RenderResource(path string, id int, startTimeString string, vars map[string]any, r *rand.Rand, seed int64, seedRandomizerEnabled bool) (pcommon.Resource, error) {
	metricsTemplate, err := RenderMetricsTemplate(path+"-resource-attributes", &resourceTemplateModel{
		InstanceID:            id,
		InstanceStartTime:     startTimeString,
		Vars:                  vars,
		rand:                  r,
		seed:                  seed,
		seedRandomizerEnabled: seedRandomizerEnabled,
	})
	if err != nil {
		return pcommon.Resource{}, err
	}
	return metricsTemplate.ResourceMetrics().At(0).Resource(), nil
}

type resourceTemplateModel struct {
	InstanceID int
	Vars       map[string]any

	InstanceStartTime     string
	rand                  *rand.Rand
	seed                  int64
	seedRandomizerEnabled bool
}

func (m *resourceTemplateModel) randByte() byte {
	return byte(m.rand.Int())
}

// GetInstanceID returns the instance ID based on the seed randomizer.
func (t *resourceTemplateModel) GetInstanceID() string {
	if !t.seedRandomizerEnabled {
		return fmt.Sprintf("%d", t.InstanceID)
	}

	return fmt.Sprintf("%d-%d", t.seed, t.InstanceID)
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

func (t *resourceTemplateModel) ModFrom(mod int, s ...string) string {
	return s[mod%len(s)]
}

func (t *resourceTemplateModel) Mod(x, y int) int {
	return x % y
}
