package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

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
	"github.com/flanksource/duty/shutdown"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
)

var (
	webhookPort          int
	enableLeaderElection bool
	operatorExecutor     bool
	k8sLogLevel          int
	Operator             = &cobra.Command{
		Use:   "operator",
		Short: "Start the kubernetes operator",
		Run:   start,
	}
)

func init() {
	ServerFlags(Operator.Flags())
	Operator.Flags().BoolVar(&operatorExecutor, "executor", true, "If false, only serve the UI and sync the configs")
	Operator.Flags().IntVar(&webhookPort, "webhookPort", 8082, "Port for webhooks ")
	Operator.Flags().IntVar(&k8sLogLevel, "k8s-log-level", -1, "Kubernetes controller log level")
	Operator.Flags().BoolVar(&enableLeaderElection, "enable-leader-election", false, "Enabling this will ensure there is only one active controller manager")
}

func start(cmd *cobra.Command, args []string) {
	ctx, closer, err := duty.Start("config-db", duty.SkipMigrationByDefaultMode)
	if err != nil {
		logger.Fatalf("failed to initialize db: %v", err.Error())
	}
	shutdown.AddHook(closer)

	leaseName := ctx.Properties().String("leader.lease.name", "config-db-operator")
	leaseNamespace := ctx.Properties().String("leader.lease.namespace", api.Namespace)
	leaseIdentity := ctx.Properties().String("leader.lease.identity", "")
	if leaseIdentity == "" {
		if hostname, err := os.Hostname(); err != nil {
			ShutdownAndExit(1, fmt.Sprintf("failed to determine hostname: %v", err))
		} else {
			leaseIdentity = hostname
		}
	}

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      leaseName,
			Namespace: leaseNamespace,
		},
		Client: ctx.Kubernetes().CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: leaseIdentity,
		},
	}

	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   ctx.Properties().Duration("leader.lease.duration", 30*time.Second),
		RenewDeadline:   15 * time.Second,
		RetryPeriod:     5 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(leadContext context.Context) {
				ctx.Infof("claimed election leadership")
				if err := run(dutyContext.NewContext(leadContext), args); err != nil {
					ShutdownAndExit(1, err.Error())
				}

				ShutdownAndExit(0, "program ran to completion")
			},
			OnStoppedLeading: func() {
				ShutdownAndExit(0, "exiting. lost election leadership.")
			},
			OnNewLeader: func(identity string) {
				if identity == leaseIdentity {
					return
				}

				ctx.Infof("another instance (%s) has become the leader", identity)
			},
		},
	})
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

	// Start the server
	go serve(dutyCtx, args)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: fmt.Sprintf("0.0.0.0:%d", metricsPort),
		Port:               9443,
		LeaderElection:     enableLeaderElection,
		LeaderElectionID:   "ca62cd4d.flanksource.com",
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
