package cmd

import (
	"fmt"
	"time"

	commonsCtx "github.com/flanksource/commons/context"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	configsv1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/controllers"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/duty"
	dutyContext "github.com/flanksource/duty/context"
	"github.com/flanksource/duty/leader"
	"github.com/flanksource/duty/shutdown"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
)

var (
	webhookPort          int
	enableLeaderElection bool
	operatorExecutor     bool
	k8sLogLevel          int
	Operator             = &cobra.Command{
		Use:   "operator",
		Short: "Start the kubernetes operator",
		RunE:  startOperator,
	}
)

func init() {
	ServerFlags(Operator.Flags())
	Operator.Flags().BoolVar(&operatorExecutor, "executor", true, "If false, only serve the UI and sync the configs")
	Operator.Flags().IntVar(&webhookPort, "webhookPort", 8082, "Port for webhooks ")
	Operator.Flags().IntVar(&k8sLogLevel, "k8s-log-level", -1, "Kubernetes controller log level")
	Operator.Flags().BoolVar(&enableLeaderElection, "enable-leader-election", false, "Enabling this will ensure there is only one active controller manager")
}

func startOperator(cmd *cobra.Command, args []string) error {
	ctx, closer, err := duty.Start(app, duty.SkipMigrationByDefaultMode)
	if err != nil {
		return fmt.Errorf("failed to initialize db: %w", err)
	}
	shutdown.AddHook(closer)

	if enableLeaderElection {
		go func() {
			err := leader.Register(ctx, app, api.Namespace, nil, nil, nil)
			if err != nil {
				shutdown.ShutdownAndExit(1, fmt.Sprintf("leader election failed: %v", err))
			}
		}()
	}

	return run(ctx, args)
}

func run(ctx dutyContext.Context, args []string) error {
	dutyCtx := dutyContext.NewContext(ctx, commonsCtx.WithTracer(otel.GetTracerProvider().Tracer(otelServiceName)))

	logger := logger.GetLogger("operator")
	logger.SetLogLevel(k8sLogLevel)

	dedupWindow := ctx.Properties().Duration("changes.dedup.window", time.Hour)
	if err := db.InitChangeFingerprintCache(ctx, dedupWindow); err != nil {
		return fmt.Errorf("failed to initialize change fingerprint cache: %w", err)
	}

	ctrl.SetLogger(logr.FromSlogHandler(logger.Handler()))
	setupLog := ctrl.Log.WithName("setup")

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configsv1.AddToScheme(scheme))

	registerJobs(ctx, args)
	go serve(dutyCtx)

	mgr, err := ctrl.NewManager(ctx.KubernetesRestConfig(), ctrl.Options{
		Scheme:                  scheme,
		MetricsBindAddress:      fmt.Sprintf("0.0.0.0:%d", metricsPort),
		Port:                    9443,
		LeaderElection:          enableLeaderElection,
		LeaderElectionNamespace: api.Namespace,
		LeaderElectionID:        "ca62cd4d.flanksource.com",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		return fmt.Errorf("unable to start manager: %w", err)
	}

	if err = (&controllers.ScrapeConfigReconciler{
		Client: mgr.GetClient(),
		DB:     dutyCtx.DB(),
		Scheme: mgr.GetScheme(),
		Log:    ctrl.Log.WithName("controllers").WithName("scrape_config"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Scraper")
		return fmt.Errorf("unable to create controller: %w", err)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		return fmt.Errorf("problem running manager: %w", err)
	}

	return nil
}
