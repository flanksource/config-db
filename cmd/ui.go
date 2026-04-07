package cmd

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"encoding/json"

	"github.com/flanksource/commons/har"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/cmd/scrapeui"
	"github.com/flanksource/duty/models"
	"github.com/spf13/cobra"
)

// jsonResults matches the JSON shape produced by `config-db run --json`.
// Fields have no json tags because clicky serializes using Go field names (PascalCase).
type jsonResults struct {
	Configs            []v1.ScrapeResult
	Changes            []models.ConfigChange
	Artifacts          []models.Artifact
	Analysis           []models.ConfigAnalysis
	Relationships      []scrapeui.UIRelationship
	ConfigMeta         map[string]scrapeui.ConfigMeta
	ExternalRoles      []models.ExternalRole
	ExternalUsers      []models.ExternalUser
	ExternalGroups     []models.ExternalGroup
	ExternalUserGroups []models.ExternalUserGroup
	ConfigAccess       []v1.ExternalConfigAccess
	ConfigAccessLogs   []v1.ExternalConfigAccessLog
	HAR                []har.Entry `json:"har,omitempty"`
}

var UI = &cobra.Command{
	Use:   "ui <results.json>",
	Short: "Launch the scrape UI to view saved JSON results",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		data, err := os.ReadFile(args[0])
		if err != nil {
			logger.Fatalf("Failed to read file: %v", err)
		}

		var results jsonResults
		if err := json.Unmarshal(data, &results); err != nil {
			logger.Fatalf("Failed to parse JSON: %v", err)
		}

		snap := scrapeui.Snapshot{
			Results: v1.FullScrapeResults{
				Configs:            results.Configs,
				Changes:            results.Changes,
				Analysis:           results.Analysis,
				ExternalRoles:      results.ExternalRoles,
				ExternalUsers:      results.ExternalUsers,
				ExternalGroups:     results.ExternalGroups,
				ExternalUserGroups: results.ExternalUserGroups,
				ConfigAccess:       results.ConfigAccess,
				ConfigAccessLogs:   results.ConfigAccessLogs,
			},
			Relationships: results.Relationships,
			ConfigMeta:    results.ConfigMeta,
			HAR:           results.HAR,
		}

		srv := scrapeui.NewStaticServer(snap)

		addr := fmt.Sprintf("localhost:%d", uiPort)
		listener, listenErr := net.Listen("tcp", addr)
		if listenErr != nil && uiPort != 0 {
			logger.Warnf("Port %d in use, picking a free port", uiPort)
			listener, listenErr = net.Listen("tcp", "localhost:0")
		}
		if listenErr != nil {
			logger.Fatalf("Failed to start UI server: %v", listenErr)
		}

		port := listener.Addr().(*net.TCPAddr).Port
		url := fmt.Sprintf("http://localhost:%d", port)

		go http.Serve(listener, srv.Handler()) //nolint:errcheck

		time.Sleep(100 * time.Millisecond)
		logger.Infof("Scrape UI at %s", url)
		openBrowser(url)

		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
	},
}

func init() {
	UI.Flags().IntVar(&uiPort, "ui-port", 9001, "Port for the UI server (0 to pick a free port)")
}
