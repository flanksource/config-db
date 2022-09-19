package cmd

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"strings"
	gotemplate "text/template"

	"github.com/flanksource/commons/text"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/pkg/errors"

	"gopkg.in/flanksource/yaml.v3"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
)

func readFile(path string) (string, error) {
	var data []byte
	var err error
	if path == "-" {
		if data, err = ioutil.ReadAll(os.Stdin); err != nil {
			return "", err
		}
	} else {
		if data, err = ioutil.ReadFile(path); err != nil {
			return "", err
		}
	}
	return string(data), nil
}

func parseDataFile(file string) (interface{}, error) {
	var d interface{}
	data, err := readFile(file)
	if err != nil {
		return nil, err
	}
	err = yaml.Unmarshal([]byte(data), &d)
	return d, err
}

func template(content string, data interface{}) (string, error) {
	tpl := gotemplate.New("")
	tpl, err := tpl.Funcs(text.GetTemplateFuncs()).Parse(content)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("error executing template %s: %v", strings.Split(content, "\n")[0], err)
	}
	fmt.Println(buf.String())
	return strings.TrimSpace(buf.String()), nil
}

func getConfigs(files []string) ([]v1.ConfigScraper, error) {
	scrapers := []v1.ConfigScraper{}

	for _, f := range files {
		_scrapers, err := ParseConfig(f, "")
		if err != nil {
			return nil, errors.Wrapf(err, "error parsing %s", f)
		}
		scrapers = append(scrapers, _scrapers...)
	}
	return scrapers, nil
}

// ParseConfig : Read config file
func ParseConfig(configfile string, datafile string) ([]v1.ConfigScraper, error) {
	configs, err := readFile(configfile)
	if err != nil {
		return nil, err
	}

	if datafile != "" {
		data, err := parseDataFile(datafile)
		if err != nil {
			return nil, err
		}
		configs, err = template(configs, data)
		if err != nil {
			return nil, err
		}
	}

	var scrapers []v1.ConfigScraper

	re := regexp.MustCompile(`(?m)^---\n`)
	for _, chunk := range re.Split(configs, -1) {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		config := v1.ConfigScraper{}
		decoder := yamlutil.NewYAMLOrJSONDecoder(strings.NewReader(chunk), 1024)

		if err := decoder.Decode(&config); err != nil {
			return nil, err
		}

		scrapers = append(scrapers, config)
	}

	return scrapers, nil
}
