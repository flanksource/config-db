package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/jobs"
	"github.com/flanksource/config-db/scrapers"
	"github.com/flanksource/config-db/scrapers/kubernetes"
	"github.com/flanksource/config-db/telemetry"
	"github.com/flanksource/config-db/utils/kube"
	"github.com/flanksource/duty"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/klog/v2"
)

var dev bool
var httpPort, metricsPort, devGuiPort int
var disableKubernetes bool
var publicEndpoint = "http://localhost:8080"
var propertiesFile = "config.properties"
var disablePostgrest bool
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var (
	// Telemetry flag vars
	otelcollectorURL string
	otelServiceName  string
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
		logger.UseZap()

		// sync the log level for klogs (used by ketall)
		var l klog.Level
		_ = l.Set(fmt.Sprintf("%d", logger.StandardLogger().GetLevel()))

		var err error
		if api.KubernetesClient, api.KubernetesRestConfig, err = kube.NewK8sClient(); err != nil {
			logger.Errorf("failed to get kubernetes client: %v", err)
		}

		db.ConnectionString = readFromEnv(db.ConnectionString)
		if db.ConnectionString == "DB_URL" {
			db.ConnectionString = ""
		}
		db.Schema = readFromEnv(db.Schema)
		db.PGRSTLogLevel = readFromEnv(db.PGRSTLogLevel)

		if otelcollectorURL != "" {
			logger.Infof("Sending traces to %s", otelcollectorURL)
			_ = telemetry.InitTracer(otelServiceName, otelcollectorURL, true) // TODO: Setup runner
		}
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
	flags.IntVar(&jobs.ConfigItemRetentionDays, "config-retention-days", jobs.DefaultConfigItemRetentionDays, "Days to retain deleted config items for")
	flags.BoolVar(&disableKubernetes, "disable-kubernetes", false, "Disable all functionality that requires a kubernetes connection")
	flags.BoolVar(&dev, "dev", false, "Run in development mode")
	flags.BoolVar(&disablePostgrest, "disable-postgrest", false, "Disable the postgrest server")
	flags.StringVar(&scrapers.DefaultSchedule, "default-schedule", "@every 60m", "Default schedule for configs that don't specfiy one")
	flags.StringVar(&publicEndpoint, "public-endpoint", "http://localhost:8080", "Public endpoint that this instance is exposed under")
	flags.IntVar(&kubernetes.BufferSize, "watch-event-buffer", kubernetes.BufferSize, "Buffer size for kubernetes events")

	flags.StringVar(&otelcollectorURL, "otel-collector-url", "", "OpenTelemetry gRPC Collector URL in host:port format")
	flags.StringVar(&otelServiceName, "otel-service-name", "config-db", "OpenTelemetry service name for the resource")

	// Flags for push/pull
	var upstreamPageSizeDefault = 500
	if val, exists := os.LookupEnv("UPSTREAM_PAGE_SIZE"); exists {
		if parsed, err := strconv.Atoi(val); err != nil || parsed <= 0 {
			logger.Fatalf("invalid value=%s for UPSTREAM_PAGE_SIZE. Must be a postive number", val)
		} else {
			upstreamPageSizeDefault = parsed
		}
	}

	flags.StringVar(&api.UpstreamConfig.Host, "upstream-host", os.Getenv("UPSTREAM_HOST"), "central mission control instance to sync scrape configs & their results")
	flags.StringVar(&api.UpstreamConfig.Username, "upstream-user", os.Getenv("UPSTREAM_USER"), "upstream username")
	flags.StringVar(&api.UpstreamConfig.Password, "upstream-password", os.Getenv("UPSTREAM_PASSWORD"), "upstream password")
	flags.StringVar(&api.UpstreamConfig.AgentName, "agent-name", os.Getenv("AGENT_NAME"), "name of this agent")
	flags.IntVar(&jobs.ReconcilePageSize, "upstream-page-size", upstreamPageSizeDefault, "upstream reconciliation page size")
}

func init() {
	logger.BindFlags(Root.PersistentFlags())
	duty.BindFlags(Root.PersistentFlags())

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
