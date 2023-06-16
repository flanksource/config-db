package cmd

import (
	"fmt"
	"os"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/jobs"
	"github.com/flanksource/config-db/scrapers"
	"github.com/flanksource/config-db/utils/kube"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	agentID   = uuid.Nil // the derived agent id from the agentName
	agentName string     // name of the agent passed as a CLI arg
)

var dev bool
var httpPort, metricsPort, devGuiPort int
var disableKubernetes bool
var publicEndpoint = "http://localhost:8080"
var disablePostgrest bool
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func readFromEnv(v string) string {
	val := os.Getenv(v)
	if val != "" {
		return val
	}
	return v
}

// Root ...
var Root = &cobra.Command{
	Use: "config-db",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		count, _ := cmd.Flags().GetCount("loglevel")
		// logger.StandardLogger().(logsrusapi.Logger).Out = os.Stderr
		logger.StandardLogger().SetLogLevel(count)
		logger.UseZap(cmd.Flags())
		var err error

		if api.KubernetesClient, api.KubernetesRestConfig, err = kube.NewK8sClient(); err != nil {
			logger.Errorf("failed to get kubernetes client: %v", err)
		}

		db.ConnectionString = readFromEnv(db.ConnectionString)
		if db.ConnectionString == "DB_URL" {
			db.ConnectionString = ""
		}
		db.Schema = readFromEnv(db.Schema)
		db.LogLevel = readFromEnv(db.LogLevel)

	},
}

// ServerFlags ...
func ServerFlags(flags *pflag.FlagSet) {
	flags.IntVar(&httpPort, "httpPort", 8080, "Port to expose a health dashboard ")
	flags.StringVar(&api.Namespace, "namespace", os.Getenv("NAMESPACE"), "Namespace to watch for config-db resources")
	flags.IntVar(&devGuiPort, "devGuiPort", 3004, "Port used by a local npm server in development mode")
	flags.IntVar(&metricsPort, "metricsPort", 8081, "Port to expose a health dashboard ")
	flags.IntVar(&jobs.ConfigAnalysisRetentionDays, "analysis-retention-days", jobs.DefaultConfigAnalysisRetentionDays, "Days to retain config analysis for")
	flags.IntVar(&jobs.ConfigChangeRetentionDays, "change-retention-days", jobs.DefaultConfigChangeRetentionDays, "Days to retain config changes for")
	flags.BoolVar(&disableKubernetes, "disable-kubernetes", false, "Disable all functionality that requires a kubernetes connection")
	flags.BoolVar(&dev, "dev", false, "Run in development mode")
	flags.BoolVar(&disablePostgrest, "disable-postgrest", false, "Disable the postgrest server")
	flags.StringVar(&scrapers.DefaultSchedule, "default-schedule", "@every 60m", "Default schedule for configs that don't specfiy one")
	flags.StringVar(&scrapers.StaleTimeout, "stale-timeout", "30m", "Delete config items not scraped within the timeout")
	flags.StringVar(&publicEndpoint, "public-endpoint", "http://localhost:8080", "Public endpoint that this instance is exposed under")
	flags.StringVar(&agentName, "agent-name", "", "Name of the agent")
}

func init() {
	logger.BindFlags(Root.PersistentFlags())

	if len(commit) > 8 {
		version = fmt.Sprintf("%v, commit %v, built at %v", version, commit[0:8], date)
	}
	Root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version of config-db",
		Args:  cobra.MinimumNArgs(0),
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(version)
		},
	})

	db.Flags(Root.PersistentFlags())

	Root.AddCommand(Run, Analyze, Serve, GoOffline, Operator)
}
