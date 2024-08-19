package cmd

import (
	"errors"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"

	commonsCtx "github.com/flanksource/commons/context"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	configsv1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/controllers"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/duty"
	dutyContext "github.com/flanksource/duty/context"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

var webhookPort int
var enableLeaderElection bool
var operatorExecutor bool
var k8sLogLevel int
var Operator = &cobra.Command{
	Use:   "operator",
	Short: "Start the kubernetes operator",
	RunE:  run,
}

func init() {
	ServerFlags(Operator.Flags())
	Operator.Flags().BoolVar(&operatorExecutor, "executor", true, "If false, only serve the UI and sync the configs")
	Operator.Flags().IntVar(&webhookPort, "webhookPort", 8082, "Port for webhooks ")
	Operator.Flags().IntVar(&k8sLogLevel, "k8s-log-level", -1, "Kubernetes controller log level")
	Operator.Flags().BoolVar(&enableLeaderElection, "enable-leader-election", false, "Enabling this will ensure there is only one active controller manager")
}

func run(cmd *cobra.Command, args []string) error {
	ctx, closer, err := duty.Start("config-db")
	if err != nil {
		logger.Fatalf("Failed to initialize db: %v", err.Error())
	}
	AddShutdownHook(closer)

	dutyCtx := dutyContext.NewContext(ctx, commonsCtx.WithTracer(otel.GetTracerProvider().Tracer(otelServiceName)))
	api.DefaultContext = api.NewScrapeContext(dutyCtx.WithKubernetes(api.KubernetesClient))

	logger := logger.GetLogger("operator")
	logger.SetLogLevel(k8sLogLevel)

	if ok, err := duty.HasMigrationsRun(ctx, api.DefaultContext.Pool()); err != nil {
		return fmt.Errorf("failed to check if migrations have run: %w", err)
	} else if !ok {
		return errors.New("migrations not run, waiting for mission-control pod to start")
	}

	dedupWindow := api.DefaultContext.Properties().Duration("changes.dedup.window", time.Hour)
	if err := db.InitChangeFingerprintCache(api.DefaultContext, dedupWindow); err != nil {
		return fmt.Errorf("failed to initialize change fingerprint cache: %w", err)
	}

	ctrl.SetLogger(logr.FromSlogHandler(logger.Handler()))
	setupLog := ctrl.Log.WithName("setup")

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configsv1.AddToScheme(scheme))

	// Start the server
	go serve(args)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: fmt.Sprintf("0.0.0.0:%d", metricsPort),
		Port:               9443,
		LeaderElection:     enableLeaderElection,
		LeaderElectionID:   "ca62cd4d.flanksource.com",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controllers.ScrapeConfigReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Log:    ctrl.Log.WithName("controllers").WithName("scrape_config"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Scraper")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}

	return nil
}
