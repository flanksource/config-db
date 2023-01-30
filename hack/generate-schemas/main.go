package main

import (
	"os"
	"path"

	"github.com/alecthomas/jsonschema"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/spf13/cobra"
)

var schemas = map[string]any{
	"scrape_config": &v1.ScrapeConfig{},
}

var generateSchema = &cobra.Command{
	Use: "generate-schema",
	Run: func(cmd *cobra.Command, args []string) {
		for file, obj := range schemas {
			schema := jsonschema.Reflect(obj)
			data, err := schema.MarshalJSON()
			if err != nil {
				logger.Fatalf("error marshalling: %v", err)
			}

			os.Mkdir(schemaPath, 0755)
			p := path.Join(schemaPath, file+".schema.json")
			if err := os.WriteFile(p, data, 0644); err != nil {
				logger.Fatalf("unable to save schema: %v", err)
			}
			logger.Infof("Saved OpenAPI schema to %s", p)
		}
	},
}

var schemaPath string

func main() {
	generateSchema.Flags().StringVar(&schemaPath, "schema-path", "../../config/schemas", "Path to save JSON schema to")
	if err := generateSchema.Execute(); err != nil {
		os.Exit(1)
	}
}
