/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	configsv1 "github.com/flanksource/config-db/api/v1"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers"
)

const ScrapeConfigFinalizerName = "scrapeConfig.config.flanksource.com"

// ScrapeConfigReconciler reconciles a ScrapeConfig object
type ScrapeConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
}

//+kubebuilder:rbac:groups=configs.flanksource.com,resources=scrapeconfigs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=configs.flanksource.com,resources=scrapeconfigs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=configs.flanksource.com,resources=scrapeconfigs/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ScrapeConfig object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.1/pkg/reconcile
func (r *ScrapeConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("scrape_config", req.NamespacedName)

	scrapeConfig := &v1.ScrapeConfig{}
	err := r.Get(ctx, req.NamespacedName, scrapeConfig)
	if err != nil {
		if errors.IsNotFound(err) {
			// log
			logger.Error(err, "msg")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Check if it is deleted, remove scrape config
	logger.Info("DeletionTimestamp", "a", scrapeConfig.DeletionTimestamp.IsZero())
	if !scrapeConfig.DeletionTimestamp.IsZero() {
		logger.Info("Deleting scrape config", "id", scrapeConfig.GetUID())
		if err := db.DeleteScrapeConfig(scrapeConfig); err != nil {
			logger.Error(err, "failed to delete scrape config")
		}
		controllerutil.RemoveFinalizer(scrapeConfig, ScrapeConfigFinalizerName)
		return ctrl.Result{}, r.Update(ctx, scrapeConfig)
	}

	// Add finalizer
	if !controllerutil.ContainsFinalizer(scrapeConfig, ScrapeConfigFinalizerName) {
		logger.Info("adding finalizer", "finalizers", scrapeConfig.GetFinalizers())
		controllerutil.AddFinalizer(scrapeConfig, ScrapeConfigFinalizerName)
		// TODO: Doing here because status update and patch are failing
		r.Update(ctx, scrapeConfig)
	}

	changed, err := db.PersistScrapeConfig(scrapeConfig)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Sync jobs if new scrape config is created
	if changed || scrapeConfig.Generation == 1 {
		if err := scrapers.RunScraper(scrapeConfig.Spec.ConfigScraper); err != nil {
			logger.Error(err, "failed to run scraper")
			return ctrl.Result{Requeue: true, RequeueAfter: 2 * time.Minute}, err
		}
		scrapers.AddToCron(scrapeConfig.Spec.ConfigScraper, string(scrapeConfig.GetUID()))
	}

	// TODO: Why is status not working ?
	scrapeConfigStatus := &v1.ScrapeConfig{}
	err = r.Get(ctx, req.NamespacedName, scrapeConfigStatus)
	scrapeConfigStatus.Status.ObservedGeneration = scrapeConfig.Generation
	r.patch(scrapeConfigStatus)

	return ctrl.Result{}, nil
}

func (r *ScrapeConfigReconciler) patch(scrapeConfig *v1.ScrapeConfig) error {
	r.Log.V(3).Info("patching", "systemTemplate", scrapeConfig.Name, "namespace", scrapeConfig.Namespace)
	if err := r.Update(context.Background(), scrapeConfig, &client.UpdateOptions{}); err != nil {
		r.Log.Error(err, "failed to patch", "systemTemplate", scrapeConfig.Name)
	}
	if err := r.Status().Update(context.Background(), scrapeConfig); err != nil {
		r.Log.Error(err, "failed to update status", "systemTemplate", scrapeConfig.Name)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ScrapeConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&configsv1.ScrapeConfig{}).
		Complete(r)
}
