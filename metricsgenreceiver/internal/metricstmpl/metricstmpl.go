package metricstmpl

import (
	"bytes"
	"path/filepath"
	"text/template"
)

func RenderMetricsTemplate(path string, templateVars map[string]any, err error) (*bytes.Buffer, error) {
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
	path += ".json"
	tpl, err := template.New(path).Funcs(funcMap).ParseFiles(path)
	if err != nil {
		return nil, err
	}

	buf := new(bytes.Buffer)
	err = tpl.ExecuteTemplate(buf, filepath.Base(path), templateVars)
	if err != nil {
		return nil, err
	}
	return buf, nil
}
