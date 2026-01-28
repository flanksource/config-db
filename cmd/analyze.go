package cmd

import (
	"encoding/json"
	"os"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/analyzers"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/spf13/cobra"
)

var outputFile, outputFormat string

// Analyzers is the analyzers registry (non-cloud analyzers)
var Analyzers = []v1.Analyzer{
	analyzers.PatchAnalyzer,
}

// ConfigUnmarshalers allows cloud-specific packages to register custom unmarshalers
var ConfigUnmarshalers = map[string]func(obj *v1.ScrapeResult) error{}

// Analyze ...
var Analyze = &cobra.Command{
	Use:   "analyze <resources>",
	Short: "Analyze configuration items and report discrepancies/issues.",
	Run: func(cmd *cobra.Command, configs []string) {
		objects := []v1.ScrapeResult{}
		for _, path := range configs {
			obj := v1.ScrapeResult{}
			data, err := os.ReadFile(path)
			if err != nil {
				logger.Fatalf("could not read %s: %v", path, err)
			}
			if err := json.Unmarshal(data, &obj); err != nil {
				logger.Fatalf("Could not unmarshall %s: %v", path, err)
			}

			if unmarshaler, ok := ConfigUnmarshalers[obj.ConfigClass]; ok {
				if err := unmarshaler(&obj); err != nil {
					logger.Fatalf("Failed to unmarshal object %s: %v", obj.ID, err)
				}
			}
			objects = append(objects, obj)
		}
		results := []v1.AnalysisResult{}
		for _, analyzer := range Analyzers {
			results = append(results, analyzer(objects))
		}
		if outputFormat == "json" {
			data, _ := json.Marshal(results)
			if err := os.WriteFile(outputFile, data, 0644); err != nil {
				logger.Fatalf("Failed to write to %s: %v", outputFile, err)
			}
		}
	},
}

func init() {
	Analyze.Flags().StringVarP(&outputFile, "output", "o", "analysis.json", "Output file")
	Analyze.Flags().StringVarP(&outputFormat, "format", "f", "json", "Output format")
}
