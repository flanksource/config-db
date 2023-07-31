package v1

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
)

var yamlDividerRegexp = regexp.MustCompile(`(?m)^---\n`)

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

func ParseConfigs(files ...string) ([]ScrapeConfig, error) {
	scrapers := make([]ScrapeConfig, 0, len(files))

	for _, f := range files {
		_scrapers, err := parseConfig(f)
		if err != nil {
			return nil, err
		}

		scrapers = append(scrapers, _scrapers...)
	}

	return scrapers, nil
}

// ParseConfig : Read config file
func parseConfig(configfile string) ([]ScrapeConfig, error) {
	configs, err := readFile(configfile)
	if err != nil {
		return nil, fmt.Errorf("error reading config file=%s: %w", configfile, err)
	}

	var scrapers []ScrapeConfig
	for _, chunk := range yamlDividerRegexp.Split(configs, -1) {
		if strings.TrimSpace(chunk) == "" {
			continue
		}

		var config ScrapeConfig
		decoder := yamlutil.NewYAMLOrJSONDecoder(strings.NewReader(chunk), 1024)
		if err := decoder.Decode(&config); err != nil {
			return nil, fmt.Errorf("error decoding yaml. file=%s: %w", configfile, err)
		}

		scrapers = append(scrapers, config)
	}

	return scrapers, nil
}
