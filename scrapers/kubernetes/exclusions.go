package kubernetes

import (
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/commons/collections"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var IgnoreCache = cache.New(1*time.Hour, 30*time.Minute)

func (ctx *KubernetesContext) getObjectChangeExclusionAnnotations(id string) (string, string, error) {
	var changeTypeExclusion, changeSeverityExclusion string
	var found = true

	if exclusion, ok := ctx.exclusionByType[id]; ok {
		changeTypeExclusion = exclusion
	} else {
		found = false
	}

	if severityExclusion, ok := ctx.exclusionBySeverity[id]; ok {
		changeSeverityExclusion = severityExclusion
	} else {
		found = false
	}

	if found && false {
		return changeTypeExclusion, changeSeverityExclusion, nil
	}

	// The requested object was not found in the scraped objects.
	// This happens on incremental scraper.
	// We query the object from the db to get the annotations;
	item, err := ctx.TempCache().Get(ctx.ScrapeContext, id)
	if err != nil {
		return "", "", err
	} else if item != nil && item.Labels != nil {
		labels := lo.FromPtr(item.Labels)
		if v, ok := labels[v1.AnnotationIgnoreChangeByType]; ok {
			changeTypeExclusion = v
		}

		if v, ok := labels[v1.AnnotationIgnoreChangeBySeverity]; ok {
			changeSeverityExclusion = v
		}
	}

	return changeTypeExclusion, changeSeverityExclusion, nil
}
func (ctx *KubernetesContext) ignore(obj *unstructured.Unstructured) {
	if ctx.logExclusions {
		ctx.Debugf("excluding object: %s/%s/%s", obj.GetKind(), obj.GetNamespace(), obj.GetName())
	}
	IgnoreCache.Set(string(obj.GetUID()), true, 0)
}

func (ctx *KubernetesContext) IsIgnored(obj *unstructured.Unstructured) (bool, error) {
	if string(obj.GetUID()) == "" {
		if ctx.logNoResourceId {
			ctx.Warnf("Found kubernetes object with no resource ID: %s/%s/%s", obj.GetKind(), obj.GetNamespace(), obj.GetName())
		}
		return true, nil
	}

	if ctx.config.Exclusions.Filter(obj.GetName(), obj.GetNamespace(), obj.GetKind(), obj.GetLabels()) {
		ctx.ignore(obj)
		return true, nil
	}

	if val, ok := obj.GetAnnotations()[v1.AnnotationIgnoreConfig]; ok && val == "true" || val == "*" {
		ctx.ignore(obj)
		return true, nil
	}
	return false, nil
}

func (ctx *KubernetesContext) IgnoreChange(change v1.ChangeResult, event v1.KubernetesEvent) (bool, error) {
	if _, ok := IgnoreCache.Get(string(event.InvolvedObject.UID)); ok {
		return true, nil
	}
	changeTypeExclusion, changeSeverityExclusion, err := ctx.getObjectChangeExclusionAnnotations(string(event.InvolvedObject.UID))
	if err != nil {
		return false, fmt.Errorf("failed to get annotation for object from db (%s): %w", event.InvolvedObject.UID, err)
	}

	if changeTypeExclusion != "" {
		if collections.MatchItems(change.ChangeType, strings.Split(changeTypeExclusion, ",")...) {
			if ctx.logExclusions {
				ctx.Tracef("excluding event object %s/%s/%s due to change type matched in annotation %s=%s",
					event.InvolvedObject.Namespace, event.InvolvedObject.Name, event.InvolvedObject.Kind,
					v1.AnnotationIgnoreChangeByType, changeTypeExclusion)
			}
			return true, nil
		}
	}

	if changeSeverityExclusion != "" {
		if collections.MatchItems(change.Severity, strings.Split(changeSeverityExclusion, ",")...) {
			if ctx.logExclusions {
				ctx.Tracef("excluding event object %s/%s/%s due to severity matches in annotation %s=%s",
					event.InvolvedObject.Namespace, event.InvolvedObject.Name, event.InvolvedObject.Kind,
					v1.AnnotationIgnoreChangeBySeverity, changeSeverityExclusion)
			}
			return true, nil
		}
	}
	return false, nil
}
