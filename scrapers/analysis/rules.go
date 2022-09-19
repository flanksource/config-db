package analysis

import (
	_ "embed"

	"github.com/flanksource/commons/logger"
	"gopkg.in/yaml.v3"
)

//go:embed rules.yaml
var configRules []byte

type Category struct {
	Category, Severity string
}

var Rules map[string]Category

func init() {
	if err := yaml.Unmarshal(configRules, &Rules); err != nil {
		logger.Errorf("Failed to unmarshal config rules: %s", err)
	}
	logger.Infof("Loaded %d config rules", len(Rules))
}
