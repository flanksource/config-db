package cmd

import (
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty/postgrest"
	"github.com/spf13/cobra"
)

// GoOffline ...
var GoOffline = &cobra.Command{
	Use:  "go-offline",
	Long: "Download all dependencies so that config-db can work without an internet connection",
	Run: func(cmd *cobra.Command, args []string) {
		if err := postgrest.GoOffline(); err != nil {
			logger.Fatalf("Failed to go offline: %+v", err)
		}
	},
}
