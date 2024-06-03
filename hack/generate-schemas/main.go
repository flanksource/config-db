package main

import (
	"fmt"
	"os"
	"path"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/schema/openapi"
	"github.com/spf13/cobra"
)

var schemas = map[string]any{
	"scrape_config": &v1.ScrapeConfig{},
}

var generateSchema = &cobra.Command{
	Use: "generate-schema",
	Run: func(cmd *cobra.Command, args []string) {
		os.Mkdir(schemaPath, 0755)
		for file, obj := range schemas {
			p := path.Join(schemaPath, file+".schema.json")
			if err := openapi.WriteSchemaToFile(p, obj); err != nil {
				logger.Fatalf("unable to save schema: %v", err)
			}
			logger.Infof("Saved OpenAPI schema to %s", p)
		}

		for name, obj := range v1.AllScraperConfigs {
			p := path.Join(schemaPath, fmt.Sprintf("config_%s.schema.json", name))
			if err := openapi.WriteSchemaToFile(p, obj); err != nil {
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
