// ABOUTME: Tests for RBAC resource extraction (Roles, RoleBindings, and their cluster variants).
// ABOUTME: Verifies ExternalRoles, ExternalUsers, ExternalGroups, and ConfigAccess are created correctly.

package kubernetes

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestRBACExtractor_ProcessRole(t *testing.T) {
	clusterName := "test-cluster"
	scraperID := uuid.New()

	tests := []struct {
		name             string
		obj              *unstructured.Unstructured
		expectedRoleName string
		expectedRoleType string
		expectedAliases  []string
	}{
		{
			name:             "ClusterRole",
			obj:              makeClusterRole("cluster-admin", []rbacRuleSpec{{Resources: []string{"pods"}}}),
			expectedRoleName: "cluster-admin",
			expectedRoleType: "ClusterRole",
			expectedAliases:  []string{KubernetesAlias(clusterName, "ClusterRole", "", "cluster-admin")},
		},
		{
			name:             "Namespaced Role",
			obj:              makeRole("pod-reader", "default", []rbacRuleSpec{{Resources: []string{"pods"}}}),
			expectedRoleName: "pod-reader",
			expectedRoleType: "Role",
			expectedAliases:  []string{KubernetesAlias(clusterName, "Role", "default", "pod-reader")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extractor := newRBACExtractor(clusterName, &scraperID)
			extractor.processRole(tt.obj)

			roles := extractor.getRoles()
			require.Len(t, roles, 1, "expected 1 role")

			role := roles[0]
			assert.Equal(t, tt.expectedRoleName, role.Name)
			assert.Equal(t, tt.expectedRoleType, role.RoleType)
			assert.Equal(t, clusterName, role.AccountID)
			assert.Equal(t, &scraperID, role.ScraperID)
			assert.Equal(t, pq.StringArray(tt.expectedAliases), role.Aliases)
		})
	}
}

func TestRBACExtractor_ProcessRoleBinding_ServiceAccount(t *testing.T) {
	clusterName := "test-cluster"
	scraperID := uuid.New()

	// Create test objects: a Role and some Pods in the namespace
	role := makeRole("pod-reader", "default", []rbacRuleSpec{
		{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
	})

	pod1 := makePod("pod-1", "default")
	pod2 := makePod("pod-2", "default")
	podOtherNS := makePod("pod-other", "other-namespace")

	binding := makeRoleBinding("my-binding", "default", "Role", "pod-reader", []subject{
		{Kind: "ServiceAccount", Name: "my-sa", Namespace: "default"},
	})

	extractor := newRBACExtractor(clusterName, &scraperID)
	extractor.indexObjects([]*unstructured.Unstructured{role, pod1, pod2, podOtherNS, binding})
	extractor.processRole(role)
	extractor.processRoleBinding(binding)

	users := extractor.getUsers()
	require.Len(t, users, 1, "expected 1 user")

	user := users[0]
	assert.Equal(t, "my-sa", user.Name)
	assert.Equal(t, "ServiceAccount", user.UserType)
	assert.Equal(t, clusterName, user.AccountID)
	assert.Equal(t, scraperID, user.ScraperID)
	expectedUserAlias := KubernetesAlias(clusterName, "ServiceAccount", "default", "my-sa")
	assert.Equal(t, pq.StringArray{expectedUserAlias}, user.Aliases)

	// Should have ConfigAccess for the 2 pods in the default namespace only
	access := extractor.getAccess()
	require.Len(t, access, 2, "expected 2 config access entries (one per pod in namespace)")

	// Check that access entries point to pods, not the role
	for _, a := range access {
		assert.Equal(t, ConfigTypePrefix+"Pod", a.ConfigExternalID.ConfigType)
		assert.Equal(t, []string{expectedUserAlias}, a.ExternalUserAliases)
	}
}

func TestRBACExtractor_ProcessRoleBinding_User(t *testing.T) {
	clusterName := "test-cluster"
	scraperID := uuid.New()

	// Create a ClusterRole that grants access to pods and services
	role := makeClusterRole("cluster-admin", []rbacRuleSpec{
		{APIGroups: []string{""}, Resources: []string{"pods", "services"}, Verbs: []string{"*"}},
	})

	pod1 := makePod("pod-1", "ns1")
	svc1 := makeService("svc-1", "ns1")

	binding := makeClusterRoleBinding("admin-binding", "ClusterRole", "cluster-admin", []subject{
		{Kind: "User", Name: "admin@example.com"},
	})

	extractor := newRBACExtractor(clusterName, &scraperID)
	extractor.indexObjects([]*unstructured.Unstructured{role, pod1, svc1, binding})
	extractor.processRole(role)
	extractor.processRoleBinding(binding)

	users := extractor.getUsers()
	require.Len(t, users, 1, "expected 1 user")

	user := users[0]
	assert.Equal(t, "admin@example.com", user.Name)
	assert.Equal(t, "User", user.UserType)
	expectedUserAlias := KubernetesAlias(clusterName, "User", "", "admin@example.com")
	assert.Equal(t, pq.StringArray{expectedUserAlias}, user.Aliases)

	// Should have ConfigAccess for the pod and service (cluster-wide)
	access := extractor.getAccess()
	assert.Len(t, access, 2, "expected 2 config access entries (pod + service)")
}

func TestRBACExtractor_ProcessRoleBinding_Group(t *testing.T) {
	clusterName := "test-cluster"
	scraperID := uuid.New()

	role := makeClusterRole("view", []rbacRuleSpec{
		{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
	})

	pod1 := makePod("pod-1", "default")

	binding := makeClusterRoleBinding("viewers-binding", "ClusterRole", "view", []subject{
		{Kind: "Group", Name: "system:authenticated"},
	})

	extractor := newRBACExtractor(clusterName, &scraperID)
	extractor.indexObjects([]*unstructured.Unstructured{role, pod1, binding})
	extractor.processRole(role)
	extractor.processRoleBinding(binding)

	groups := extractor.getGroups()
	require.Len(t, groups, 1, "expected 1 group")

	group := groups[0]
	assert.Equal(t, "system:authenticated", group.Name)
	assert.Equal(t, "Group", group.GroupType)
	assert.Equal(t, clusterName, group.AccountID)
	expectedGroupAlias := KubernetesAlias(clusterName, "Group", "", "system:authenticated")
	assert.Equal(t, pq.StringArray{expectedGroupAlias}, group.Aliases)

	access := extractor.getAccess()
	require.Len(t, access, 1, "expected 1 config access entry")
	assert.Empty(t, access[0].ExternalUserAliases)
	assert.Equal(t, []string{expectedGroupAlias}, access[0].ExternalGroupAliases)
}

func TestRBACExtractor_ProcessRoleBinding_MixedSubjects(t *testing.T) {
	clusterName := "test-cluster"
	scraperID := uuid.New()

	role := makeClusterRole("edit", []rbacRuleSpec{
		{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"*"}},
	})

	pod1 := makePod("pod-1", "default")

	binding := makeClusterRoleBinding("mixed-binding", "ClusterRole", "edit", []subject{
		{Kind: "ServiceAccount", Name: "ci-bot", Namespace: "ci"},
		{Kind: "User", Name: "developer@example.com"},
		{Kind: "Group", Name: "developers"},
	})

	extractor := newRBACExtractor(clusterName, &scraperID)
	extractor.indexObjects([]*unstructured.Unstructured{role, pod1, binding})
	extractor.processRole(role)
	extractor.processRoleBinding(binding)

	users := extractor.getUsers()
	assert.Len(t, users, 2, "expected 2 users (SA + User)")

	groups := extractor.getGroups()
	assert.Len(t, groups, 1, "expected 1 group")

	// Each subject gets one ConfigAccess entry for the pod
	access := extractor.getAccess()
	assert.Len(t, access, 3, "expected 3 config access entries (one per subject, all pointing to same pod)")
}

func TestRBACExtractor_Deduplication(t *testing.T) {
	clusterName := "test-cluster"
	scraperID := uuid.New()

	extractor := newRBACExtractor(clusterName, &scraperID)

	// Process the same role twice
	role := makeClusterRole("cluster-admin", []rbacRuleSpec{{Resources: []string{"pods"}}})
	extractor.processRole(role)
	extractor.processRole(role)

	roles := extractor.getRoles()
	assert.Len(t, roles, 1, "duplicate roles should be deduplicated")
}

func TestRBACExtractor_NamespaceScoping(t *testing.T) {
	clusterName := "test-cluster"
	scraperID := uuid.New()

	// A Role in namespace "default" granting access to pods
	role := makeRole("pod-reader", "default", []rbacRuleSpec{
		{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
	})

	// Pods in different namespaces
	podDefault1 := makePod("pod-1", "default")
	podDefault2 := makePod("pod-2", "default")
	podOther := makePod("pod-other", "other")

	// RoleBinding in default namespace
	binding := makeRoleBinding("my-binding", "default", "Role", "pod-reader", []subject{
		{Kind: "User", Name: "user1"},
	})

	extractor := newRBACExtractor(clusterName, &scraperID)
	extractor.indexObjects([]*unstructured.Unstructured{role, podDefault1, podDefault2, podOther, binding})
	extractor.processRole(role)
	extractor.processRoleBinding(binding)

	// Should only have access to pods in "default" namespace
	access := extractor.getAccess()
	assert.Len(t, access, 2, "should only have access to 2 pods in default namespace")

	for _, a := range access {
		// Verify the config external ID contains "default" namespace
		assert.Contains(t, a.ConfigExternalID.ExternalID, "/default/")
	}
}

// Helper types and functions

type subject struct {
	Kind      string
	Name      string
	Namespace string
}

type rbacRuleSpec struct {
	APIGroups []string
	Resources []string
	Verbs     []string
}

func makeClusterRole(name string, rules []rbacRuleSpec) *unstructured.Unstructured {
	rulesData := make([]any, len(rules))
	for i, r := range rules {
		rule := map[string]any{}
		if len(r.APIGroups) > 0 {
			apiGroups := make([]any, len(r.APIGroups))
			for j, ag := range r.APIGroups {
				apiGroups[j] = ag
			}
			rule["apiGroups"] = apiGroups
		}
		if len(r.Resources) > 0 {
			resources := make([]any, len(r.Resources))
			for j, res := range r.Resources {
				resources[j] = res
			}
			rule["resources"] = resources
		}
		if len(r.Verbs) > 0 {
			verbs := make([]any, len(r.Verbs))
			for j, v := range r.Verbs {
				verbs[j] = v
			}
			rule["verbs"] = verbs
		}
		rulesData[i] = rule
	}

	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind":       "ClusterRole",
			"metadata": map[string]any{
				"uid":               uuid.NewString(),
				"name":              name,
				"creationTimestamp": time.Now().Format(time.RFC3339),
			},
			"rules": rulesData,
		},
	}
}

func makeRole(name, namespace string, rules []rbacRuleSpec) *unstructured.Unstructured {
	obj := makeClusterRole(name, rules)
	obj.Object["kind"] = "Role"
	obj.Object["metadata"].(map[string]any)["namespace"] = namespace
	return obj
}

func makeRoleBinding(name, namespace, roleKind, roleName string, subjects []subject) *unstructured.Unstructured {
	subjectsMap := make([]any, len(subjects))
	for i, s := range subjects {
		subj := map[string]any{
			"kind": s.Kind,
			"name": s.Name,
		}
		if s.Namespace != "" {
			subj["namespace"] = s.Namespace
		}
		if s.Kind == "ServiceAccount" {
			subj["apiGroup"] = ""
		} else {
			subj["apiGroup"] = "rbac.authorization.k8s.io"
		}
		subjectsMap[i] = subj
	}

	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind":       "RoleBinding",
			"metadata": map[string]any{
				"uid":               uuid.NewString(),
				"name":              name,
				"namespace":         namespace,
				"creationTimestamp": time.Now().Format(time.RFC3339),
			},
			"subjects": subjectsMap,
			"roleRef": map[string]any{
				"apiGroup": "rbac.authorization.k8s.io",
				"kind":     roleKind,
				"name":     roleName,
			},
		},
	}
}

func makeClusterRoleBinding(name, roleKind, roleName string, subjects []subject) *unstructured.Unstructured {
	subjectsMap := make([]any, len(subjects))
	for i, s := range subjects {
		subj := map[string]any{
			"kind": s.Kind,
			"name": s.Name,
		}
		if s.Namespace != "" {
			subj["namespace"] = s.Namespace
		}
		if s.Kind == "ServiceAccount" {
			subj["apiGroup"] = ""
		} else {
			subj["apiGroup"] = "rbac.authorization.k8s.io"
		}
		subjectsMap[i] = subj
	}

	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind":       "ClusterRoleBinding",
			"metadata": map[string]any{
				"uid":               uuid.NewString(),
				"name":              name,
				"creationTimestamp": time.Now().Format(time.RFC3339),
			},
			"subjects": subjectsMap,
			"roleRef": map[string]any{
				"apiGroup": "rbac.authorization.k8s.io",
				"kind":     roleKind,
				"name":     roleName,
			},
		},
	}
}

func makePod(name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]any{
				"uid":               uuid.NewString(),
				"name":              name,
				"namespace":         namespace,
				"creationTimestamp": time.Now().Format(time.RFC3339),
			},
		},
	}
}

func makeService(name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]any{
				"uid":               uuid.NewString(),
				"name":              name,
				"namespace":         namespace,
				"creationTimestamp": time.Now().Format(time.RFC3339),
			},
		},
	}
}
