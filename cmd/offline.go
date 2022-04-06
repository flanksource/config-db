package cmd

import (
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/confighub/db"
	"github.com/spf13/cobra"
)

// GoOffline ...
var GoOffline = &cobra.Command{
	Use:  "go-offline",
	Long: "Download all dependencies so that confighub can work without an internet connection",
	Run: func(cmd *cobra.Command, args []string) {
		if err := db.GoOffline(); err != nil {
			logger.Fatalf("Failed to go offline: %+v", err)
		}
	},
}
