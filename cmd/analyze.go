package cmd

import (
	"encoding/json"
	"io/ioutil"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/confighub/analyzers"
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/scrapers/aws"
	"github.com/spf13/cobra"
)

var outputFile, outputFormat string

// Analyzers ...
var Analyzers = []v1.Analyzer{
	analyzers.PatchAnalyzer,
	aws.EC2InstanceAnalyzer,
}

// Analyze ...
var Analyze = &cobra.Command{
	Use:   "analyze <resources>",
	Short: "Analyze configuration items and report discrepencies/issues.",
	Run: func(cmd *cobra.Command, configs []string) {

		objects := []v1.ScrapeResult{}
		for _, path := range configs {
			obj := v1.ScrapeResult{}
			data, err := ioutil.ReadFile(path)
			if err != nil {
				logger.Fatalf("could not read %s: %v", path, err)
			}
			if err := json.Unmarshal(data, &obj); err != nil {
				logger.Fatalf("Could not unmarshall %s: %v", path, err)
			}

			if obj.Type == "EC2Instance" {
				nested, _ := json.Marshal(obj.Config)
				instance := aws.Instance{}
				if err := json.Unmarshal(nested, &instance); err != nil {
					logger.Fatalf("Failed to unmarshal object into ec2 instance %s", obj.Id)
				}
				obj.Config = instance
			}
			objects = append(objects, obj)
		}
		results := []v1.AnalysisResult{}
		for _, analyzer := range Analyzers {
			results = append(results, analyzer(objects))
		}
		if outputFormat == "json" {
			data, _ := json.Marshal(results)
			if err := ioutil.WriteFile(outputFile, data, 0644); err != nil {
				logger.Fatalf("Failed to write to %s: %v", outputFile, err)
			}
		}
	},
}

func init() {
	Analyze.Flags().StringVarP(&outputFile, "output", "o", "analysis.json", "Output file")
	Analyze.Flags().StringVarP(&outputFormat, "format", "f", "json", "Output format")

}
