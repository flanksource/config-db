// ABOUTME: Extracts RBAC resources (Roles, ClusterRoles, RoleBindings, ClusterRoleBindings) for config access.
// ABOUTME: Creates ExternalRoles, ExternalUsers, ExternalGroups, and ConfigAccess entries from Kubernetes RBAC.

package kubernetes

import (
	"strings"
	"sync"
	"time"

	"github.com/flanksource/commons/collections"
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
	exclusions  v1.ScraperExclusion
	roles       map[uuid.UUID]models.ExternalRole
	users       map[uuid.UUID]models.ExternalUser
	groups      map[uuid.UUID]models.ExternalGroup
	access     []v1.ExternalConfigAccess
	seenAccess map[string]struct{} // dedup key for access entries

	roleRules      map[string][]rbacRule // key: kind/namespace/name -> rules
	resourceToKind map[string]string     // plural resource name -> Kind (e.g., "pods" -> "Pod")
	ignoredRoles   map[string]bool       // key: kind/namespace/name -> true if role is excluded
}

type rbacRule struct {
	APIGroups     []string
	Resources     []string
	Verbs         []string
	ResourceNames []string
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

func newRBACExtractor(ctx api.ScrapeContext, clusterName string, scraperID *uuid.UUID, exclusions v1.ScraperExclusion) *rbacExtractor {
	if scraperID == nil {
		ctx.Warnf("Ignoring RBAC Extraction due to empty scraperID")
		return nil
	}

	// Start with built-in resource mappings
	resourceMap := make(map[string]string, len(builtinResourceKinds))
	for k, v := range builtinResourceKinds {
		resourceMap[k] = v
	}

	// Merge CRD mappings from the K8s API
	for k, v := range fetchCRDResourceKinds(ctx, clusterName) {
		resourceMap[k] = v
	}

	return newRBACExtractorWithResourceMap(clusterName, scraperID, resourceMap, exclusions)
}

func newRBACExtractorWithResourceMap(clusterName string, scraperID *uuid.UUID, resourceToKind map[string]string, exclusions v1.ScraperExclusion) *rbacExtractor {
	return &rbacExtractor{
		clusterName:    clusterName,
		scraperID:      scraperID,
		exclusions:     exclusions,
		roles:          make(map[uuid.UUID]models.ExternalRole),
		users:          make(map[uuid.UUID]models.ExternalUser),
		groups:         make(map[uuid.UUID]models.ExternalGroup),
		roleRules:      make(map[string][]rbacRule),
		seenAccess:     make(map[string]struct{}),
		resourceToKind: resourceToKind,
		ignoredRoles:   make(map[string]bool),
	}
}

func (r *rbacExtractor) objectKey(kind, namespace, name string) string {
	return kind + "/" + namespace + "/" + name
}

func (r *rbacExtractor) processRole(obj *unstructured.Unstructured) {
	if r == nil {
		return
	}
	kind := obj.GetKind()
	if kind != "ClusterRole" && kind != "Role" {
		return
	}

	name := obj.GetName()
	namespace := obj.GetNamespace()

	if len(r.exclusions.ExternalRoles) > 0 && collections.MatchItems(name, r.exclusions.ExternalRoles...) {
		key := r.objectKey(kind, namespace, name)
		r.ignoredRoles[key] = true
		// Still parse and store the rules so bindings can resolve correctly,
		// but don't create the ExternalRole entry.
		rules := r.parseRules(obj)
		r.roleRules[key] = rules
		return
	}

	id := generateRBACID(r.clusterName, kind, namespace, name)
	alias := KubernetesAlias(r.clusterName, kind, namespace, name)

	role := models.ExternalRole{
		ID:        id,
		Name:      name,
		Tenant:    r.clusterName,
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

		if resourceNames, ok := ruleMap["resourceNames"].([]any); ok {
			for _, rn := range resourceNames {
				if s, ok := rn.(string); ok {
					rule.ResourceNames = append(rule.ResourceNames, s)
				}
			}
		}

		rules = append(rules, rule)
	}

	return rules
}

func (r *rbacExtractor) processRoleBinding(obj *unstructured.Unstructured) {
	if r == nil {
		return
	}
	kind := obj.GetKind()
	if kind != "ClusterRoleBinding" && kind != "RoleBinding" {
		return
	}

	isClusterWide := kind == "ClusterRoleBinding"
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

	// Lookup the role's rules; skip if the role was not scraped or is excluded
	roleKey := r.objectKey(roleKind, roleNamespace, roleName)
	if r.ignoredRoles[roleKey] {
		return
	}

	rules, hasRules := r.roleRules[roleKey]
	if !hasRules || len(rules) == 0 {
		return
	}

	// Determine the scope-level target (cluster or namespace)
	var scopeExternalID, scopeConfigType string
	if isClusterWide {
		scopeExternalID = "Kubernetes/Cluster/" + r.clusterName
		scopeConfigType = ConfigTypePrefix + "Cluster"
	} else {
		scopeExternalID = KubernetesAlias(r.clusterName, "Namespace", "", bindingNamespace)
		scopeConfigType = ConfigTypePrefix + "Namespace"
	}

	// Collect explicitly named resources from rules
	namedResources := r.collectNamedResources(rules, bindingNamespace, isClusterWide)

	roleAlias := KubernetesAlias(r.clusterName, roleKind, roleNamespace, roleName)

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

		// Skip excluded users (ServiceAccount, User) and groups
		switch subjKind {
		case "ServiceAccount", "User":
			if len(r.exclusions.ExternalUsers) > 0 && collections.MatchItems(subjName, r.exclusions.ExternalUsers...) {
				continue
			}
		case "Group":
			if len(r.exclusions.ExternalGroups) > 0 && collections.MatchItems(subjName, r.exclusions.ExternalGroups...) {
				continue
			}
		}

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
					Tenant:    r.clusterName,
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
					Tenant:    r.clusterName,
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
					Tenant:    r.clusterName,
					ScraperID: *r.scraperID,
					Aliases:   pq.StringArray{alias},
					CreatedAt: time.Now(),
				}
			}
		}

		subjectAlias := userAlias
		if subjectAlias == "" {
			subjectAlias = groupAlias
		}
		if subjectAlias == "" {
			continue
		}

		// Always create access for the scope (cluster or namespace)
		r.addAccess(subjectAlias, scopeExternalID, scopeConfigType, roleAlias, userAlias, groupAlias)

		// Create access for each explicitly named resource
		for _, nr := range namedResources {
			r.addAccess(subjectAlias, nr.externalID, nr.configType, roleAlias, userAlias, groupAlias)
		}
	}
}

type namedResource struct {
	externalID string
	configType string
}

// collectNamedResources extracts explicitly named resources from RBAC rules.
func (r *rbacExtractor) collectNamedResources(rules []rbacRule, bindingNamespace string, isClusterWide bool) []namedResource {
	var resources []namedResource

	for _, rule := range rules {
		if len(rule.ResourceNames) == 0 {
			continue
		}

		for _, resourceType := range rule.Resources {
			kind, ok := r.resourceToKind[strings.ToLower(resourceType)]
			if !ok {
				continue
			}

			// For cluster-scoped bindings, named resources have no namespace
			// For namespace-scoped bindings, named resources are in the binding's namespace
			namespace := ""
			if !isClusterWide {
				namespace = bindingNamespace
			}

			for _, name := range rule.ResourceNames {
				resources = append(resources, namedResource{
					externalID: KubernetesAlias(r.clusterName, kind, namespace, name),
					configType: GetConfigTypeForKind(kind),
				})
			}
		}
	}

	return resources
}

func (r *rbacExtractor) addAccess(subjectAlias, targetExternalID, targetConfigType, roleAlias, userAlias, groupAlias string) {
	key := subjectAlias + "|" + targetExternalID + "|" + roleAlias
	if _, seen := r.seenAccess[key]; seen {
		return
	}
	r.seenAccess[key] = struct{}{}

	access := v1.ExternalConfigAccess{
		ID: generateRBACID(subjectAlias, targetExternalID, roleAlias).String(),
		ConfigExternalID: v1.ExternalID{
			ExternalID: targetExternalID,
			ConfigType: targetConfigType,
		},
		ExternalRoleAliases: []string{roleAlias},
	}

	if userAlias != "" {
		access.ExternalUserAliases = []string{userAlias}
	}
	if groupAlias != "" {
		access.ExternalGroupAliases = []string{groupAlias}
	}

	r.access = append(r.access, access)
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

// pruneOrphanedUsers removes users that have no corresponding access entries.
func (r *rbacExtractor) pruneOrphanedUsers() {
	usedAliases := make(map[string]bool)
	for _, a := range r.access {
		for _, alias := range a.ExternalUserAliases {
			usedAliases[alias] = true
		}
	}

	for id, user := range r.users {
		hasAccess := false
		for _, alias := range user.Aliases {
			if usedAliases[alias] {
				hasAccess = true
				break
			}
		}
		if !hasAccess {
			delete(r.users, id)
		}
	}
}

func (r *rbacExtractor) results(baseScraper v1.BaseScraper) v1.ScrapeResult {
	if r == nil {
		return v1.ScrapeResult{}
	}

	r.pruneOrphanedUsers()

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
