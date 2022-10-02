package v1

import (
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/pkg/errors"

	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
)

func readFile(path string) (string, error) {
	var data []byte
	var err error
	if path == "-" {
		if data, err = io.ReadAll(os.Stdin); err != nil {
			return "", err
		}
	} else {
		if data, err = os.ReadFile(path); err != nil {
			return "", err
		}
	}
	return string(data), nil
}

func ParseConfigs(files ...string) ([]ConfigScraper, error) {
	scrapers := []ConfigScraper{}

	for _, f := range files {
		_scrapers, err := parseConfig(f)
		if err != nil {
			return nil, errors.Wrapf(err, "error parsing %s", f)
		}
		scrapers = append(scrapers, _scrapers...)
	}
	return scrapers, nil
}

// ParseConfig : Read config file
func parseConfig(configfile string) ([]ConfigScraper, error) {
	configs, err := readFile(configfile)
	if err != nil {
		return nil, err
	}

	var scrapers []ConfigScraper

	re := regexp.MustCompile(`(?m)^---\n`)
	for _, chunk := range re.Split(configs, -1) {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		config := ConfigScraper{}
		decoder := yamlutil.NewYAMLOrJSONDecoder(strings.NewReader(chunk), 1024)

		if err := decoder.Decode(&config); err != nil {
			return nil, err
		}

		scrapers = append(scrapers, config)
	}

	return scrapers, nil
}
