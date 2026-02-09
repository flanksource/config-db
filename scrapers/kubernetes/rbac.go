// ABOUTME: Extracts RBAC resources (Roles, ClusterRoles, RoleBindings, ClusterRoleBindings) for config access.
// ABOUTME: Creates ExternalRoles, ExternalUsers, ExternalGroups, and ConfigAccess entries from Kubernetes RBAC.

package kubernetes

import (
	"strings"
	"sync"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	uuidV5 "github.com/gofrs/uuid/v5"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type rbacExtractor struct {
	clusterName string
	scraperID   *uuid.UUID
	roles       map[uuid.UUID]models.ExternalRole
	users       map[uuid.UUID]models.ExternalUser
	groups      map[uuid.UUID]models.ExternalGroup
	access      []v1.ExternalConfigAccess

	// Maps for lookups
	roleRules       map[string][]rbacRule    // key: kind/namespace/name -> rules
	objectsByKind   map[string][]*objectRef  // key: kind -> list of objects
	resourceToKind  map[string]string        // plural resource name -> Kind (e.g., "pods" -> "Pod")
}

type rbacRule struct {
	APIGroups []string
	Resources []string
	Verbs     []string
}

type objectRef struct {
	obj       *unstructured.Unstructured
	kind      string
	namespace string
	name      string
}

// builtinResourceKinds maps plural resource names to their Kind for core Kubernetes resources.
var builtinResourceKinds = map[string]string{
	"pods":                     "Pod",
	"services":                 "Service",
	"deployments":              "Deployment",
	"replicasets":              "ReplicaSet",
	"statefulsets":             "StatefulSet",
	"daemonsets":               "DaemonSet",
	"jobs":                     "Job",
	"cronjobs":                 "CronJob",
	"configmaps":               "ConfigMap",
	"secrets":                  "Secret",
	"persistentvolumeclaims":   "PersistentVolumeClaim",
	"persistentvolumes":        "PersistentVolume",
	"namespaces":               "Namespace",
	"nodes":                    "Node",
	"serviceaccounts":          "ServiceAccount",
	"ingresses":                "Ingress",
	"networkpolicies":          "NetworkPolicy",
	"roles":                    "Role",
	"rolebindings":             "RoleBinding",
	"clusterroles":             "ClusterRole",
	"clusterrolebindings":      "ClusterRoleBinding",
	"events":                   "Event",
	"endpoints":                "Endpoints",
	"limitranges":              "LimitRange",
	"resourcequotas":           "ResourceQuota",
	"poddisruptionbudgets":     "PodDisruptionBudget",
	"horizontalpodautoscalers": "HorizontalPodAutoscaler",
}

var crdResourceKindCache = struct {
	sync.Mutex
	entries map[string]crdCacheEntry
}{
	entries: make(map[string]crdCacheEntry),
}

type crdCacheEntry struct {
	resourceToKind map[string]string
	expiresAt      time.Time
}

const crdCacheTTL = 12 * time.Hour

// fetchCRDResourceKinds queries the K8s API for CRDs and returns a resource→kind map.
// Results are cached per cluster for 12 hours.
func fetchCRDResourceKinds(ctx api.ScrapeContext, clusterName string) map[string]string {
	crdResourceKindCache.Lock()
	defer crdResourceKindCache.Unlock()

	if entry, ok := crdResourceKindCache.entries[clusterName]; ok && time.Now().Before(entry.expiresAt) {
		return entry.resourceToKind
	}

	resourceMap := make(map[string]string)

	if ctx.KubernetesConnection() == nil {
		ctx.Debugf("no kubernetes connection available, skipping CRD lookup")
		return resourceMap
	}

	k8s, err := ctx.Kubernetes()
	if err != nil {
		ctx.Warnf("failed to get k8s client for CRD lookup: %v", err)
		return resourceMap
	}

	if k8s == nil || k8s.RestConfig() == nil {
		ctx.Warnf("kubernetes client or rest config is nil, skipping CRD lookup")
		return resourceMap
	}

	cs, err := clientset.NewForConfig(k8s.RestConfig())
	if err != nil {
		ctx.Warnf("failed to create apiextensions client for CRD lookup: %v", err)
		return resourceMap
	}

	allCRDs, err := cs.ApiextensionsV1().CustomResourceDefinitions().List(ctx, metav1.ListOptions{})
	if err != nil {
		ctx.Warnf("failed to list CRDs: %v", err)
		return resourceMap
	}

	for _, crd := range allCRDs.Items {
		plural := crd.Spec.Names.Plural
		kind := crd.Spec.Names.Kind
		if plural != "" && kind != "" {
			resourceMap[strings.ToLower(plural)] = kind
		}
	}

	crdResourceKindCache.entries[clusterName] = crdCacheEntry{
		resourceToKind: resourceMap,
		expiresAt:      time.Now().Add(crdCacheTTL),
	}

	return resourceMap
}

func newRBACExtractor(ctx api.ScrapeContext, clusterName string, scraperID *uuid.UUID) *rbacExtractor {
	// Start with built-in resource mappings
	resourceMap := make(map[string]string, len(builtinResourceKinds))
	for k, v := range builtinResourceKinds {
		resourceMap[k] = v
	}

	// Merge CRD mappings from the K8s API
	for k, v := range fetchCRDResourceKinds(ctx, clusterName) {
		resourceMap[k] = v
	}

	return newRBACExtractorWithResourceMap(clusterName, scraperID, resourceMap)
}

func newRBACExtractorWithResourceMap(clusterName string, scraperID *uuid.UUID, resourceToKind map[string]string) *rbacExtractor {
	return &rbacExtractor{
		clusterName:    clusterName,
		scraperID:      scraperID,
		roles:          make(map[uuid.UUID]models.ExternalRole),
		users:          make(map[uuid.UUID]models.ExternalUser),
		groups:         make(map[uuid.UUID]models.ExternalGroup),
		roleRules:      make(map[string][]rbacRule),
		objectsByKind:  make(map[string][]*objectRef),
		resourceToKind: resourceToKind,
	}
}

// indexObjects builds lookup maps for all scraped objects.
func (r *rbacExtractor) indexObjects(objs []*unstructured.Unstructured) {
	for _, obj := range objs {
		kind := obj.GetKind()

		r.objectsByKind[kind] = append(r.objectsByKind[kind], &objectRef{
			obj:       obj,
			kind:      kind,
			namespace: obj.GetNamespace(),
			name:      obj.GetName(),
		})
	}
}

func (r *rbacExtractor) objectKey(kind, namespace, name string) string {
	return kind + "/" + namespace + "/" + name
}

func (r *rbacExtractor) processRole(obj *unstructured.Unstructured) {
	kind := obj.GetKind()
	if kind != "ClusterRole" && kind != "Role" {
		return
	}

	name := obj.GetName()
	namespace := obj.GetNamespace()

	id := generateRBACID(r.clusterName, kind, namespace, name)
	alias := KubernetesAlias(r.clusterName, kind, namespace, name)

	role := models.ExternalRole{
		ID:        id,
		Name:      name,
		AccountID: r.clusterName,
		ScraperID: r.scraperID,
		RoleType:  kind,
		Aliases:   pq.StringArray{alias},
		CreatedAt: time.Now(),
	}

	r.roles[id] = role

	// Parse and store the rules for later lookup
	rules := r.parseRules(obj)
	key := r.objectKey(kind, namespace, name)
	r.roleRules[key] = rules
}

func (r *rbacExtractor) parseRules(obj *unstructured.Unstructured) []rbacRule {
	var rules []rbacRule

	rulesSlice, found, _ := unstructured.NestedSlice(obj.Object, "rules")
	if !found {
		return rules
	}

	for _, ruleRaw := range rulesSlice {
		ruleMap, ok := ruleRaw.(map[string]any)
		if !ok {
			continue
		}

		rule := rbacRule{}

		if apiGroups, ok := ruleMap["apiGroups"].([]any); ok {
			for _, ag := range apiGroups {
				if s, ok := ag.(string); ok {
					rule.APIGroups = append(rule.APIGroups, s)
				}
			}
		}

		if resources, ok := ruleMap["resources"].([]any); ok {
			for _, res := range resources {
				if s, ok := res.(string); ok {
					rule.Resources = append(rule.Resources, s)
				}
			}
		}

		if verbs, ok := ruleMap["verbs"].([]any); ok {
			for _, v := range verbs {
				if s, ok := v.(string); ok {
					rule.Verbs = append(rule.Verbs, s)
				}
			}
		}

		rules = append(rules, rule)
	}

	return rules
}

func (r *rbacExtractor) processRoleBinding(obj *unstructured.Unstructured) {
	kind := obj.GetKind()
	if kind != "ClusterRoleBinding" && kind != "RoleBinding" {
		return
	}

	bindingNamespace := obj.GetNamespace()

	// Get roleRef
	roleRef, found, _ := unstructured.NestedMap(obj.Object, "roleRef")
	if !found {
		return
	}

	roleKind, _, _ := unstructured.NestedString(roleRef, "kind")
	roleName, _, _ := unstructured.NestedString(roleRef, "name")

	// For Roles, they're in the same namespace as the RoleBinding
	// For ClusterRoles referenced by RoleBindings, namespace is empty
	roleNamespace := ""
	if roleKind == "Role" {
		roleNamespace = bindingNamespace
	}

	// Lookup the role's rules
	roleKey := r.objectKey(roleKind, roleNamespace, roleName)
	rules, hasRules := r.roleRules[roleKey]

	// Find all target resources based on the rules
	targetResources := r.findTargetResources(rules, bindingNamespace, kind == "ClusterRoleBinding")

	// Get subjects
	subjects, found, _ := unstructured.NestedSlice(obj.Object, "subjects")
	if !found {
		return
	}

	for _, subj := range subjects {
		subjMap, ok := subj.(map[string]any)
		if !ok {
			continue
		}

		subjKind, _ := subjMap["kind"].(string)
		subjName, _ := subjMap["name"].(string)
		subjNamespace, _ := subjMap["namespace"].(string)

		var userAlias, groupAlias string

		switch subjKind {
		case "ServiceAccount":
			id := generateRBACID(r.clusterName, "ServiceAccount", subjNamespace, subjName)
			alias := KubernetesAlias(r.clusterName, "ServiceAccount", subjNamespace, subjName)
			userAlias = alias

			if _, exists := r.users[id]; !exists {
				r.users[id] = models.ExternalUser{
					ID:        id,
					Name:      subjName,
					UserType:  "ServiceAccount",
					AccountID: r.clusterName,
					ScraperID: *r.scraperID,
					Aliases:   pq.StringArray{alias},
					CreatedAt: time.Now(),
				}
			}

		case "User":
			id := generateRBACID(r.clusterName, "User", "", subjName)
			alias := KubernetesAlias(r.clusterName, "User", "", subjName)
			userAlias = alias

			if _, exists := r.users[id]; !exists {
				r.users[id] = models.ExternalUser{
					ID:        id,
					Name:      subjName,
					UserType:  "User",
					AccountID: r.clusterName,
					ScraperID: *r.scraperID,
					Aliases:   pq.StringArray{alias},
					CreatedAt: time.Now(),
				}
			}

		case "Group":
			id := generateRBACID(r.clusterName, "Group", "", subjName)
			alias := KubernetesAlias(r.clusterName, "Group", "", subjName)
			groupAlias = alias

			if _, exists := r.groups[id]; !exists {
				r.groups[id] = models.ExternalGroup{
					ID:        id,
					Name:      subjName,
					GroupType: "Group",
					AccountID: r.clusterName,
					ScraperID: *r.scraperID,
					Aliases:   pq.StringArray{alias},
					CreatedAt: time.Now(),
				}
			}
		}

		// If we have rules and target resources, create ConfigAccess for each resource
		if hasRules && len(targetResources) > 0 {
			for _, target := range targetResources {
				access := v1.ExternalConfigAccess{
					ConfigExternalID: v1.ExternalID{
						ExternalID: KubernetesAlias(r.clusterName, target.kind, target.namespace, target.name),
						ConfigType: GetConfigTypeForKind(target.kind),
					},
				}

				if userAlias != "" {
					access.ExternalUserAliases = []string{userAlias}
				}
				if groupAlias != "" {
					access.ExternalGroupAliases = []string{groupAlias}
				}

				r.access = append(r.access, access)
			}
		}
	}
}

// findTargetResources finds all resources that match the given RBAC rules.
func (r *rbacExtractor) findTargetResources(rules []rbacRule, bindingNamespace string, isClusterWide bool) []*objectRef {
	var targets []*objectRef

	for _, rule := range rules {
		for _, resourceType := range rule.Resources {
			if resourceType == "*" {
				continue
			}

			kind, ok := r.resourceToKind[strings.ToLower(resourceType)]
			if !ok {
				continue
			}

			for _, objRef := range r.objectsByKind[kind] {
				// For namespace-scoped bindings, only include resources in the same namespace
				if !isClusterWide && objRef.namespace != bindingNamespace {
					continue
				}
				targets = append(targets, objRef)
			}
		}
	}

	return targets
}

// GetConfigTypeForKind returns the config type for a given Kubernetes kind
func GetConfigTypeForKind(kind string) string {
	return ConfigTypePrefix + kind
}

func (r *rbacExtractor) getRoles() []models.ExternalRole {
	roles := make([]models.ExternalRole, 0, len(r.roles))
	for _, role := range r.roles {
		roles = append(roles, role)
	}
	return roles
}

func (r *rbacExtractor) getUsers() []models.ExternalUser {
	users := make([]models.ExternalUser, 0, len(r.users))
	for _, user := range r.users {
		users = append(users, user)
	}
	return users
}

func (r *rbacExtractor) getGroups() []models.ExternalGroup {
	groups := make([]models.ExternalGroup, 0, len(r.groups))
	for _, group := range r.groups {
		groups = append(groups, group)
	}
	return groups
}

func (r *rbacExtractor) getAccess() []v1.ExternalConfigAccess {
	return r.access
}

func (r *rbacExtractor) results(baseScraper v1.BaseScraper) v1.ScrapeResult {
	return v1.ScrapeResult{
		BaseScraper:    baseScraper,
		ExternalRoles:  r.getRoles(),
		ExternalUsers:  r.getUsers(),
		ExternalGroups: r.getGroups(),
		ConfigAccess:   r.getAccess(),
	}
}

func generateRBACID(parts ...string) uuid.UUID {
	input := strings.Join(parts, "/")
	gen := uuidV5.NewV5(uuidV5.NamespaceOID, input)
	return uuid.UUID(gen)
}
