package kubernetes

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var _ = Describe("RBACExtractor", func() {
	Describe("ProcessRole", func() {
		var (
			clusterName = "test-cluster"
			scraperID   = uuid.New()
		)

		DescribeTable("creates role with correct attributes",
			func(obj *unstructured.Unstructured, expectedName, expectedType string, expectedAliases []string) {
				extractor := testRBACExtractor(clusterName, &scraperID)
				extractor.processRole(obj)

				roles := extractor.getRoles()
				Expect(roles).To(HaveLen(1))

				role := roles[0]
				Expect(role.Name).To(Equal(expectedName))
				Expect(role.RoleType).To(Equal(expectedType))
				Expect(role.Tenant).To(Equal(clusterName))
				Expect(role.ScraperID).To(Equal(&scraperID))
				Expect(role.Aliases).To(Equal(pq.StringArray(expectedAliases)))
			},
			Entry("ClusterRole",
				makeClusterRole("cluster-admin", []rbacRuleSpec{{Resources: []string{"pods"}}}),
				"cluster-admin", "ClusterRole",
				[]string{KubernetesAlias("test-cluster", "ClusterRole", "", "cluster-admin")}),
			Entry("Namespaced Role",
				makeRole("pod-reader", "default", []rbacRuleSpec{{Resources: []string{"pods"}}}),
				"pod-reader", "Role",
				[]string{KubernetesAlias("test-cluster", "Role", "default", "pod-reader")}),
		)
	})

	Describe("ProcessRoleBinding", func() {
		Context("RoleBinding without resourceNames maps to namespace", func() {
			It("creates user and a single namespace-level config access entry", func() {
				clusterName := "test-cluster"
				scraperID := uuid.New()

				role := makeRole("pod-reader", "default", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
				})
				binding := makeRoleBinding("my-binding", "default", "Role", "pod-reader", []subject{
					{Kind: "ServiceAccount", Name: "my-sa", Namespace: "default"},
				})

				extractor := testRBACExtractor(clusterName, &scraperID)
				extractor.processRole(role)
				extractor.processRoleBinding(binding)

				users := extractor.getUsers()
				Expect(users).To(HaveLen(1))

				user := users[0]
				Expect(user.Name).To(Equal("my-sa"))
				Expect(user.UserType).To(Equal("ServiceAccount"))
				expectedUserAlias := KubernetesAlias(clusterName, "ServiceAccount", "default", "my-sa")
				Expect(user.Aliases).To(Equal(pq.StringArray{expectedUserAlias}))

				access := extractor.getAccess()
				Expect(access).To(HaveLen(1), "should have exactly 1 access entry for the namespace")

				a := access[0]
				expectedNSAlias := KubernetesAlias(clusterName, "Namespace", "", "default")
				Expect(a.ConfigExternalID.ExternalID).To(Equal(expectedNSAlias))
				Expect(a.ConfigExternalID.ConfigType).To(Equal(ConfigTypePrefix + "Namespace"))
				Expect(a.ExternalUserAliases).To(Equal([]string{expectedUserAlias}))

				expectedRoleAlias := KubernetesAlias(clusterName, "Role", "default", "pod-reader")
				Expect(a.ExternalRoleAliases).To(Equal([]string{expectedRoleAlias}))
			})
		})

		Context("RoleBinding with resourceNames maps to namespace AND named resources", func() {
			It("creates access entries for namespace and each named resource", func() {
				clusterName := "test-cluster"
				scraperID := uuid.New()

				// Role "abc" in namespace "ns" gives access to deployment "d" and service "s"
				role := makeRole("abc", "ns", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"deployments"}, Verbs: []string{"get"}, ResourceNames: []string{"d"}},
					{APIGroups: []string{""}, Resources: []string{"services"}, Verbs: []string{"get"}, ResourceNames: []string{"s"}},
				})
				binding := makeRoleBinding("binding", "ns", "Role", "abc", []subject{
					{Kind: "User", Name: "dev@example.com"},
				})

				extractor := testRBACExtractor(clusterName, &scraperID)
				extractor.processRole(role)
				extractor.processRoleBinding(binding)

				access := extractor.getAccess()
				Expect(access).To(HaveLen(3), "should have ns + d + s = 3 entries")

				expectedRoleAlias := KubernetesAlias(clusterName, "Role", "ns", "abc")
				expectedUserAlias := KubernetesAlias(clusterName, "User", "", "dev@example.com")

				// Collect external IDs for verification
				var externalIDs []string
				for _, a := range access {
					externalIDs = append(externalIDs, a.ConfigExternalID.ExternalID)
					Expect(a.ExternalRoleAliases).To(Equal([]string{expectedRoleAlias}))
					Expect(a.ExternalUserAliases).To(Equal([]string{expectedUserAlias}))
				}

				expectedNS := KubernetesAlias(clusterName, "Namespace", "", "ns")
				expectedD := KubernetesAlias(clusterName, "Deployment", "ns", "d")
				expectedS := KubernetesAlias(clusterName, "Service", "ns", "s")
				Expect(externalIDs).To(ContainElements(expectedNS, expectedD, expectedS))
			})
		})

		Context("RoleBinding with mixed rules (some with resourceNames, some without)", func() {
			It("creates namespace entry plus named resource entries", func() {
				clusterName := "test-cluster"
				scraperID := uuid.New()

				// Role "def" in namespace "n2" gives access to all ingresses + specific configmaps "cm1" and "cm2"
				role := makeRole("def", "n2", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"ingresses"}, Verbs: []string{"get"}},
					{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"get"}, ResourceNames: []string{"cm1", "cm2"}},
				})
				binding := makeRoleBinding("binding", "n2", "Role", "def", []subject{
					{Kind: "User", Name: "ops@example.com"},
				})

				extractor := testRBACExtractor(clusterName, &scraperID)
				extractor.processRole(role)
				extractor.processRoleBinding(binding)

				access := extractor.getAccess()
				Expect(access).To(HaveLen(3), "should have n2 + cm1 + cm2 = 3 entries")

				var externalIDs []string
				for _, a := range access {
					externalIDs = append(externalIDs, a.ConfigExternalID.ExternalID)
				}

				expectedNS := KubernetesAlias(clusterName, "Namespace", "", "n2")
				expectedCM1 := KubernetesAlias(clusterName, "ConfigMap", "n2", "cm1")
				expectedCM2 := KubernetesAlias(clusterName, "ConfigMap", "n2", "cm2")
				Expect(externalIDs).To(ContainElements(expectedNS, expectedCM1, expectedCM2))
			})
		})

		Context("ClusterRoleBinding without resourceNames maps to cluster", func() {
			It("creates user and cluster-level config access entry", func() {
				clusterName := "test-cluster"
				scraperID := uuid.New()

				role := makeClusterRole("cluster-admin", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"pods", "services"}, Verbs: []string{"*"}},
				})
				binding := makeClusterRoleBinding("admin-binding", "ClusterRole", "cluster-admin", []subject{
					{Kind: "User", Name: "admin@example.com"},
				})

				extractor := testRBACExtractor(clusterName, &scraperID)
				extractor.processRole(role)
				extractor.processRoleBinding(binding)

				users := extractor.getUsers()
				Expect(users).To(HaveLen(1))
				Expect(users[0].Name).To(Equal("admin@example.com"))

				access := extractor.getAccess()
				Expect(access).To(HaveLen(1), "should have exactly 1 access entry for the cluster")

				a := access[0]
				expectedClusterID := "Kubernetes/Cluster/" + clusterName
				Expect(a.ConfigExternalID.ExternalID).To(Equal(expectedClusterID))
				Expect(a.ConfigExternalID.ConfigType).To(Equal(ConfigTypePrefix + "Cluster"))

				expectedUserAlias := KubernetesAlias(clusterName, "User", "", "admin@example.com")
				Expect(a.ExternalUserAliases).To(Equal([]string{expectedUserAlias}))

				expectedRoleAlias := KubernetesAlias(clusterName, "ClusterRole", "", "cluster-admin")
				Expect(a.ExternalRoleAliases).To(Equal([]string{expectedRoleAlias}))
			})
		})

		Context("ClusterRoleBinding with resourceNames maps to cluster AND named resources", func() {
			It("creates cluster entry plus named resource entries", func() {
				clusterName := "test-cluster"
				scraperID := uuid.New()

				role := makeClusterRole("node-reader", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"nodes"}, Verbs: []string{"get"}, ResourceNames: []string{"node-1", "node-2"}},
				})
				binding := makeClusterRoleBinding("node-binding", "ClusterRole", "node-reader", []subject{
					{Kind: "User", Name: "ops@example.com"},
				})

				extractor := testRBACExtractor(clusterName, &scraperID)
				extractor.processRole(role)
				extractor.processRoleBinding(binding)

				access := extractor.getAccess()
				Expect(access).To(HaveLen(3), "should have cluster + node-1 + node-2 = 3 entries")

				var externalIDs []string
				for _, a := range access {
					externalIDs = append(externalIDs, a.ConfigExternalID.ExternalID)
				}

				expectedCluster := "Kubernetes/Cluster/" + clusterName
				expectedNode1 := KubernetesAlias(clusterName, "Node", "", "node-1")
				expectedNode2 := KubernetesAlias(clusterName, "Node", "", "node-2")
				Expect(externalIDs).To(ContainElements(expectedCluster, expectedNode1, expectedNode2))
			})
		})

		Context("with Group subject", func() {
			It("creates group and cluster-level config access entry", func() {
				clusterName := "test-cluster"
				scraperID := uuid.New()

				role := makeClusterRole("view", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
				})
				binding := makeClusterRoleBinding("viewers-binding", "ClusterRole", "view", []subject{
					{Kind: "Group", Name: "system:authenticated"},
				})

				extractor := testRBACExtractor(clusterName, &scraperID)
				extractor.processRole(role)
				extractor.processRoleBinding(binding)

				groups := extractor.getGroups()
				Expect(groups).To(HaveLen(1))

				group := groups[0]
				Expect(group.Name).To(Equal("system:authenticated"))
				expectedGroupAlias := KubernetesAlias(clusterName, "Group", "", "system:authenticated")
				Expect(group.Aliases).To(Equal(pq.StringArray{expectedGroupAlias}))

				access := extractor.getAccess()
				Expect(access).To(HaveLen(1))
				Expect(access[0].ExternalUserAliases).To(BeEmpty())
				Expect(access[0].ExternalGroupAliases).To(Equal([]string{expectedGroupAlias}))

				expectedRoleAlias := KubernetesAlias(clusterName, "ClusterRole", "", "view")
				Expect(access[0].ExternalRoleAliases).To(Equal([]string{expectedRoleAlias}))

				expectedClusterID := "Kubernetes/Cluster/" + clusterName
				Expect(access[0].ConfigExternalID.ExternalID).To(Equal(expectedClusterID))
				Expect(access[0].ConfigExternalID.ConfigType).To(Equal(ConfigTypePrefix + "Cluster"))
			})
		})

		Context("with mixed subjects", func() {
			It("creates users, groups, and per-subject config access entries", func() {
				clusterName := "test-cluster"
				scraperID := uuid.New()

				role := makeClusterRole("edit", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"*"}},
				})
				binding := makeClusterRoleBinding("mixed-binding", "ClusterRole", "edit", []subject{
					{Kind: "ServiceAccount", Name: "ci-bot", Namespace: "ci"},
					{Kind: "User", Name: "developer@example.com"},
					{Kind: "Group", Name: "developers"},
				})

				extractor := testRBACExtractor(clusterName, &scraperID)
				extractor.processRole(role)
				extractor.processRoleBinding(binding)

				Expect(extractor.getUsers()).To(HaveLen(2))
				Expect(extractor.getGroups()).To(HaveLen(1))

				access := extractor.getAccess()
				Expect(access).To(HaveLen(3), "one cluster-level entry per subject")

				expectedRoleAlias := KubernetesAlias(clusterName, "ClusterRole", "", "edit")
				expectedClusterID := "Kubernetes/Cluster/" + clusterName
				for _, a := range access {
					Expect(a.ExternalRoleAliases).To(Equal([]string{expectedRoleAlias}))
					Expect(a.ConfigExternalID.ExternalID).To(Equal(expectedClusterID))
					Expect(a.ID).ToNot(BeEmpty())

					hasUser := len(a.ExternalUserAliases) > 0
					hasGroup := len(a.ExternalGroupAliases) > 0
					Expect(hasUser || hasGroup).To(BeTrue(),
						"access row should have at least one of user or group aliases")
					Expect(hasUser && hasGroup).To(BeFalse(),
						"access row should not have both user and group aliases")
				}
			})
		})

		Context("with only resourceNames rules (all explicit)", func() {
			It("still includes the namespace/cluster entry", func() {
				clusterName := "test-cluster"
				scraperID := uuid.New()

				role := makeRole("explicit-only", "myns", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}, ResourceNames: []string{"pod-a"}},
				})
				binding := makeRoleBinding("binding", "myns", "Role", "explicit-only", []subject{
					{Kind: "User", Name: "user1"},
				})

				extractor := testRBACExtractor(clusterName, &scraperID)
				extractor.processRole(role)
				extractor.processRoleBinding(binding)

				access := extractor.getAccess()
				Expect(access).To(HaveLen(2), "namespace + pod-a = 2 entries")

				var externalIDs []string
				for _, a := range access {
					externalIDs = append(externalIDs, a.ConfigExternalID.ExternalID)
				}

				expectedNS := KubernetesAlias(clusterName, "Namespace", "", "myns")
				expectedPod := KubernetesAlias(clusterName, "Pod", "myns", "pod-a")
				Expect(externalIDs).To(ContainElements(expectedNS, expectedPod))
			})
		})
	})

	Describe("Deduplication", func() {
		It("deduplicates identical roles", func() {
			clusterName := "test-cluster"
			scraperID := uuid.New()

			extractor := testRBACExtractor(clusterName, &scraperID)
			role := makeClusterRole("cluster-admin", []rbacRuleSpec{{Resources: []string{"pods"}}})
			extractor.processRole(role)
			extractor.processRole(role)

			Expect(extractor.getRoles()).To(HaveLen(1))
		})
	})

	Describe("CRDResourceResolution", func() {
		It("resolves custom resource types with resourceNames", func() {
			clusterName := "test-cluster"
			scraperID := uuid.New()

			resourceMap := make(map[string]string, len(builtinResourceKinds))
			for k, v := range builtinResourceKinds {
				resourceMap[k] = v
			}
			resourceMap["canaries"] = "Canary"

			role := makeClusterRole("canary-admin", []rbacRuleSpec{
				{APIGroups: []string{"flanksource.com"}, Resources: []string{"canaries"}, Verbs: []string{"*"}, ResourceNames: []string{"my-canary"}},
			})
			binding := makeClusterRoleBinding("canary-binding", "ClusterRole", "canary-admin", []subject{
				{Kind: "User", Name: "ops@example.com"},
			})

			extractor := newRBACExtractorWithResourceMap(clusterName, &scraperID, resourceMap)
			extractor.processRole(role)
			extractor.processRoleBinding(binding)

			access := extractor.getAccess()
			Expect(access).To(HaveLen(2), "cluster + my-canary = 2 entries")

			var externalIDs []string
			var configTypes []string
			for _, a := range access {
				externalIDs = append(externalIDs, a.ConfigExternalID.ExternalID)
				configTypes = append(configTypes, a.ConfigExternalID.ConfigType)
			}

			expectedCluster := "Kubernetes/Cluster/" + clusterName
			expectedCanary := KubernetesAlias(clusterName, "Canary", "", "my-canary")
			Expect(externalIDs).To(ContainElements(expectedCluster, expectedCanary))
			Expect(configTypes).To(ContainElement(ConfigTypePrefix + "Canary"))

			expectedRoleAlias := KubernetesAlias(clusterName, "ClusterRole", "", "canary-admin")
			for _, a := range access {
				Expect(a.ExternalRoleAliases).To(Equal([]string{expectedRoleAlias}))
			}
		})
	})
})

// Helper types and functions

func testRBACExtractor(clusterName string, scraperID *uuid.UUID) *rbacExtractor {
	resourceMap := make(map[string]string, len(builtinResourceKinds))
	for k, v := range builtinResourceKinds {
		resourceMap[k] = v
	}
	return newRBACExtractorWithResourceMap(clusterName, scraperID, resourceMap)
}

type subject struct {
	Kind      string
	Name      string
	Namespace string
}

type rbacRuleSpec struct {
	APIGroups     []string
	Resources     []string
	Verbs         []string
	ResourceNames []string
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
		if len(r.ResourceNames) > 0 {
			resourceNames := make([]any, len(r.ResourceNames))
			for j, rn := range r.ResourceNames {
				resourceNames[j] = rn
			}
			rule["resourceNames"] = resourceNames
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
