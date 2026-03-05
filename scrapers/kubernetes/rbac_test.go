package kubernetes

import (
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
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
		Context("with ServiceAccount subject", func() {
			It("creates user and scoped config access entries", func() {
				clusterName := "test-cluster"
				scraperID := uuid.New()

				role := makeRole("pod-reader", "default", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
				})
				pod1 := makePod("pod-1", "default")
				pod2 := makePod("pod-2", "default")
				podOtherNS := makePod("pod-other", "other-namespace")
				binding := makeRoleBinding("my-binding", "default", "Role", "pod-reader", []subject{
					{Kind: "ServiceAccount", Name: "my-sa", Namespace: "default"},
				})

				extractor := testRBACExtractor(clusterName, &scraperID)
				extractor.indexObjects([]*unstructured.Unstructured{role, pod1, pod2, podOtherNS, binding})
				extractor.processRole(role)
				extractor.processRoleBinding(binding)

				users := extractor.getUsers()
				Expect(users).To(HaveLen(1))

				user := users[0]
				Expect(user.Name).To(Equal("my-sa"))
				Expect(user.UserType).To(Equal("ServiceAccount"))
				Expect(user.Tenant).To(Equal(clusterName))
				Expect(user.ScraperID).To(Equal(scraperID))
				expectedUserAlias := KubernetesAlias(clusterName, "ServiceAccount", "default", "my-sa")
				Expect(user.Aliases).To(Equal(pq.StringArray{expectedUserAlias}))

				access := extractor.getAccess()
				Expect(access).To(HaveLen(2))

				expectedRoleAlias := KubernetesAlias(clusterName, "Role", "default", "pod-reader")
				for _, a := range access {
					Expect(a.ConfigExternalID.ConfigType).To(Equal(ConfigTypePrefix + "Pod"))
					Expect(a.ExternalUserAliases).To(Equal([]string{expectedUserAlias}))
					Expect(a.ExternalRoleAliases).To(Equal([]string{expectedRoleAlias}))
					Expect(a.ID).ToNot(BeEmpty())
				}
			})
		})

		Context("with User subject", func() {
			It("creates user and cluster-wide config access entries", func() {
				clusterName := "test-cluster"
				scraperID := uuid.New()

				role := makeClusterRole("cluster-admin", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"pods", "services"}, Verbs: []string{"*"}},
				})
				pod1 := makePod("pod-1", "ns1")
				svc1 := makeService("svc-1", "ns1")
				binding := makeClusterRoleBinding("admin-binding", "ClusterRole", "cluster-admin", []subject{
					{Kind: "User", Name: "admin@example.com"},
				})

				extractor := testRBACExtractor(clusterName, &scraperID)
				extractor.indexObjects([]*unstructured.Unstructured{role, pod1, svc1, binding})
				extractor.processRole(role)
				extractor.processRoleBinding(binding)

				users := extractor.getUsers()
				Expect(users).To(HaveLen(1))

				user := users[0]
				Expect(user.Name).To(Equal("admin@example.com"))
				Expect(user.UserType).To(Equal("User"))
				Expect(user.Tenant).To(Equal(clusterName))
				Expect(user.ScraperID).To(Equal(scraperID))
				expectedUserAlias := KubernetesAlias(clusterName, "User", "", "admin@example.com")
				Expect(user.Aliases).To(Equal(pq.StringArray{expectedUserAlias}))

				access := extractor.getAccess()
				Expect(access).To(HaveLen(2))

				expectedRoleAlias := KubernetesAlias(clusterName, "ClusterRole", "", "cluster-admin")
				for _, a := range access {
					Expect(a.ExternalUserAliases).To(Equal([]string{expectedUserAlias}))
					Expect(a.ExternalRoleAliases).To(Equal([]string{expectedRoleAlias}))
					Expect(a.ExternalGroupAliases).To(BeEmpty(), "User-subject should not have group aliases")
					Expect(a.ID).ToNot(BeEmpty())
				}
			})
		})

		Context("with Group subject", func() {
			It("creates group and config access entries", func() {
				clusterName := "test-cluster"
				scraperID := uuid.New()

				role := makeClusterRole("view", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
				})
				pod1 := makePod("pod-1", "default")
				binding := makeClusterRoleBinding("viewers-binding", "ClusterRole", "view", []subject{
					{Kind: "Group", Name: "system:authenticated"},
				})

				extractor := testRBACExtractor(clusterName, &scraperID)
				extractor.indexObjects([]*unstructured.Unstructured{role, pod1, binding})
				extractor.processRole(role)
				extractor.processRoleBinding(binding)

				groups := extractor.getGroups()
				Expect(groups).To(HaveLen(1))

				group := groups[0]
				Expect(group.Name).To(Equal("system:authenticated"))
				Expect(group.GroupType).To(Equal("Group"))
				Expect(group.Tenant).To(Equal(clusterName))
				expectedGroupAlias := KubernetesAlias(clusterName, "Group", "", "system:authenticated")
				Expect(group.Aliases).To(Equal(pq.StringArray{expectedGroupAlias}))

				access := extractor.getAccess()
				Expect(access).To(HaveLen(1))
				Expect(access[0].ExternalUserAliases).To(BeEmpty())
				Expect(access[0].ExternalGroupAliases).To(Equal([]string{expectedGroupAlias}))

				expectedRoleAlias := KubernetesAlias(clusterName, "ClusterRole", "", "view")
				Expect(access[0].ExternalRoleAliases).To(Equal([]string{expectedRoleAlias}))
				Expect(access[0].ID).ToNot(BeEmpty())
			})
		})

		Context("with mixed subjects", func() {
			It("creates users, groups, and per-subject config access entries", func() {
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

				extractor := testRBACExtractor(clusterName, &scraperID)
				extractor.indexObjects([]*unstructured.Unstructured{role, pod1, binding})
				extractor.processRole(role)
				extractor.processRoleBinding(binding)

				Expect(extractor.getUsers()).To(HaveLen(2))
				Expect(extractor.getGroups()).To(HaveLen(1))

				access := extractor.getAccess()
				Expect(access).To(HaveLen(3))

				expectedRoleAlias := KubernetesAlias(clusterName, "ClusterRole", "", "edit")
				for _, a := range access {
					Expect(a.ExternalRoleAliases).To(Equal([]string{expectedRoleAlias}))
					Expect(a.ID).ToNot(BeEmpty())

					// Each access row should have exactly one subject-alias bucket populated
					hasUser := len(a.ExternalUserAliases) > 0
					hasGroup := len(a.ExternalGroupAliases) > 0
					Expect(hasUser || hasGroup).To(BeTrue(),
						"access row should have at least one of user or group aliases")
					Expect(hasUser && hasGroup).To(BeFalse(),
						"access row should not have both user and group aliases")
				}
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

	Describe("NamespaceScoping", func() {
		It("restricts access to the binding namespace", func() {
			clusterName := "test-cluster"
			scraperID := uuid.New()

			role := makeRole("pod-reader", "default", []rbacRuleSpec{
				{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
			})
			podDefault1 := makePod("pod-1", "default")
			podDefault2 := makePod("pod-2", "default")
			podOther := makePod("pod-other", "other")
			binding := makeRoleBinding("my-binding", "default", "Role", "pod-reader", []subject{
				{Kind: "User", Name: "user1"},
			})

			extractor := testRBACExtractor(clusterName, &scraperID)
			extractor.indexObjects([]*unstructured.Unstructured{role, podDefault1, podDefault2, podOther, binding})
			extractor.processRole(role)
			extractor.processRoleBinding(binding)

			access := extractor.getAccess()
			Expect(access).To(HaveLen(2))

			for _, a := range access {
				Expect(a.ConfigExternalID.ExternalID).To(ContainSubstring("/default/"))
			}
		})
	})

	Describe("Exclusions", func() {
		var (
			clusterName = "test-cluster"
			scraperID   = uuid.New()
		)

		Context("role exclusion by exact name", func() {
			It("excludes the role and cascades to binding subjects", func() {
				exclusions := v1.KubernetesExclusionConfig{
					Roles: []string{"system:controller:job-controller"},
				}

				role := makeClusterRole("system:controller:job-controller", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
				})
				pod := makePod("pod-1", "default")
				binding := makeClusterRoleBinding("job-controller-binding", "ClusterRole", "system:controller:job-controller", []subject{
					{Kind: "ServiceAccount", Name: "job-controller", Namespace: "kube-system"},
				})

				extractor := testRBACExtractorWithExclusions(clusterName, &scraperID, exclusions)
				extractor.indexObjects([]*unstructured.Unstructured{role, pod, binding})
				extractor.processRole(role)
				extractor.processRoleBinding(binding)

				Expect(extractor.getRoles()).To(BeEmpty())
				Expect(extractor.getUsers()).To(BeEmpty())
				Expect(extractor.getAccess()).To(BeEmpty())
			})
		})

		Context("role exclusion by wildcard", func() {
			It("excludes multiple roles matching the pattern", func() {
				exclusions := v1.KubernetesExclusionConfig{
					Roles: []string{"system:controller:*"},
				}

				role1 := makeClusterRole("system:controller:job-controller", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
				})
				role2 := makeClusterRole("system:controller:deployment-controller", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
				})
				validRole := makeClusterRole("admin", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"*"}},
				})
				pod := makePod("pod-1", "default")
				binding1 := makeClusterRoleBinding("b1", "ClusterRole", "system:controller:job-controller", []subject{
					{Kind: "ServiceAccount", Name: "job-controller", Namespace: "kube-system"},
				})
				binding2 := makeClusterRoleBinding("b2", "ClusterRole", "system:controller:deployment-controller", []subject{
					{Kind: "ServiceAccount", Name: "deployment-controller", Namespace: "kube-system"},
				})
				binding3 := makeClusterRoleBinding("b3", "ClusterRole", "admin", []subject{
					{Kind: "User", Name: "admin@example.com"},
				})

				extractor := testRBACExtractorWithExclusions(clusterName, &scraperID, exclusions)
				extractor.indexObjects([]*unstructured.Unstructured{role1, role2, validRole, pod, binding1, binding2, binding3})
				extractor.processRole(role1)
				extractor.processRole(role2)
				extractor.processRole(validRole)
				extractor.processRoleBinding(binding1)
				extractor.processRoleBinding(binding2)
				extractor.processRoleBinding(binding3)

				roles := extractor.getRoles()
				Expect(roles).To(HaveLen(1))
				Expect(roles[0].Name).To(Equal("admin"))

				users := extractor.getUsers()
				Expect(users).To(HaveLen(1))
				Expect(users[0].Name).To(Equal("admin@example.com"))

				access := extractor.getAccess()
				Expect(access).To(HaveLen(1))
			})
		})

		Context("SA referenced by both ignored and non-ignored roles", func() {
			It("keeps the SA with only non-ignored access entries", func() {
				exclusions := v1.KubernetesExclusionConfig{
					Roles: []string{"system:controller:*"},
				}

				ignoredRole := makeClusterRole("system:controller:foo", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
				})
				validRole := makeClusterRole("pod-reader", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
				})
				pod := makePod("pod-1", "default")
				ignoredBinding := makeClusterRoleBinding("b-ignored", "ClusterRole", "system:controller:foo", []subject{
					{Kind: "ServiceAccount", Name: "shared-sa", Namespace: "default"},
				})
				validBinding := makeClusterRoleBinding("b-valid", "ClusterRole", "pod-reader", []subject{
					{Kind: "ServiceAccount", Name: "shared-sa", Namespace: "default"},
				})

				extractor := testRBACExtractorWithExclusions(clusterName, &scraperID, exclusions)
				extractor.indexObjects([]*unstructured.Unstructured{ignoredRole, validRole, pod, ignoredBinding, validBinding})
				extractor.processRole(ignoredRole)
				extractor.processRole(validRole)
				extractor.processRoleBinding(ignoredBinding)
				extractor.processRoleBinding(validBinding)

				roles := extractor.getRoles()
				Expect(roles).To(HaveLen(1))
				Expect(roles[0].Name).To(Equal("pod-reader"))

				users := extractor.getUsers()
				Expect(users).To(HaveLen(1))
				Expect(users[0].Name).To(Equal("shared-sa"))

				access := extractor.getAccess()
				Expect(access).To(HaveLen(1))

				expectedRoleAlias := KubernetesAlias(clusterName, "ClusterRole", "", "pod-reader")
				Expect(access[0].ExternalRoleAliases).To(Equal([]string{expectedRoleAlias}))
			})
		})

		Context("SA only referenced by ignored roles", func() {
			It("prunes the SA from results", func() {
				exclusions := v1.KubernetesExclusionConfig{
					Roles: []string{"system:controller:*"},
				}

				role := makeClusterRole("system:controller:foo", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
				})
				pod := makePod("pod-1", "default")
				binding := makeClusterRoleBinding("b1", "ClusterRole", "system:controller:foo", []subject{
					{Kind: "ServiceAccount", Name: "orphan-sa", Namespace: "kube-system"},
				})

				extractor := testRBACExtractorWithExclusions(clusterName, &scraperID, exclusions)
				extractor.indexObjects([]*unstructured.Unstructured{role, pod, binding})
				extractor.processRole(role)
				extractor.processRoleBinding(binding)

				Expect(extractor.getRoles()).To(BeEmpty())
				Expect(extractor.getUsers()).To(BeEmpty())
				Expect(extractor.getAccess()).To(BeEmpty())
			})
		})

		Context("user exclusion pattern", func() {
			It("excludes matching users and their access entries", func() {
				exclusions := v1.KubernetesExclusionConfig{
					Users: []string{"system:kube-*"},
				}

				role := makeClusterRole("view", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
				})
				pod := makePod("pod-1", "default")
				binding := makeClusterRoleBinding("b1", "ClusterRole", "view", []subject{
					{Kind: "User", Name: "system:kube-controller-manager"},
					{Kind: "User", Name: "admin@example.com"},
				})

				extractor := testRBACExtractorWithExclusions(clusterName, &scraperID, exclusions)
				extractor.indexObjects([]*unstructured.Unstructured{role, pod, binding})
				extractor.processRole(role)
				extractor.processRoleBinding(binding)

				users := extractor.getUsers()
				Expect(users).To(HaveLen(1))
				Expect(users[0].Name).To(Equal("admin@example.com"))

				access := extractor.getAccess()
				Expect(access).To(HaveLen(1))
				expectedUserAlias := KubernetesAlias(clusterName, "User", "", "admin@example.com")
				Expect(access[0].ExternalUserAliases).To(Equal([]string{expectedUserAlias}))
			})
		})

		Context("group exclusion pattern", func() {
			It("excludes matching groups and their access entries", func() {
				exclusions := v1.KubernetesExclusionConfig{
					Groups: []string{"system:*"},
				}

				role := makeClusterRole("view", []rbacRuleSpec{
					{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
				})
				pod := makePod("pod-1", "default")
				binding := makeClusterRoleBinding("b1", "ClusterRole", "view", []subject{
					{Kind: "Group", Name: "system:authenticated"},
					{Kind: "Group", Name: "developers"},
				})

				extractor := testRBACExtractorWithExclusions(clusterName, &scraperID, exclusions)
				extractor.indexObjects([]*unstructured.Unstructured{role, pod, binding})
				extractor.processRole(role)
				extractor.processRoleBinding(binding)

				groups := extractor.getGroups()
				Expect(groups).To(HaveLen(1))
				Expect(groups[0].Name).To(Equal("developers"))

				access := extractor.getAccess()
				Expect(access).To(HaveLen(1))
				expectedGroupAlias := KubernetesAlias(clusterName, "Group", "", "developers")
				Expect(access[0].ExternalGroupAliases).To(Equal([]string{expectedGroupAlias}))
			})
		})

		Context("orphaned SA with no access", func() {
			It("prunes users with no access entries", func() {
				role := makeClusterRole("empty-role", []rbacRuleSpec{
					// role has no resources that match any indexed objects
					{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"get"}},
				})
				// No configmaps indexed, so no target resources will be found
				binding := makeClusterRoleBinding("b1", "ClusterRole", "empty-role", []subject{
					{Kind: "ServiceAccount", Name: "unused-sa", Namespace: "default"},
				})

				extractor := testRBACExtractorWithExclusions(clusterName, &scraperID, v1.KubernetesExclusionConfig{})
				extractor.indexObjects([]*unstructured.Unstructured{role, binding})
				extractor.processRole(role)
				extractor.processRoleBinding(binding)

				// The SA was created during processRoleBinding, but has no access entries
				// because the role's resources (configmaps) don't match any indexed objects.
				// results() calls pruneOrphanedUsers which should remove it.
				result := extractor.results(v1.BaseScraper{})
				Expect(result.ExternalUsers).To(BeEmpty())
				Expect(result.ConfigAccess).To(BeEmpty())
			})
		})
	})

	Describe("CRDResourceResolution", func() {
		It("resolves custom resource types and creates access entries", func() {
			clusterName := "test-cluster"
			scraperID := uuid.New()

			resourceMap := make(map[string]string, len(builtinResourceKinds))
			for k, v := range builtinResourceKinds {
				resourceMap[k] = v
			}
			resourceMap["canaries"] = "Canary"

			canary := makeCustomResource("Canary", "my-canary", "default")
			role := makeClusterRole("canary-admin", []rbacRuleSpec{
				{APIGroups: []string{"flanksource.com"}, Resources: []string{"canaries"}, Verbs: []string{"*"}},
			})
			binding := makeClusterRoleBinding("canary-binding", "ClusterRole", "canary-admin", []subject{
				{Kind: "User", Name: "ops@example.com"},
			})

			extractor := newRBACExtractorWithResourceMap(clusterName, &scraperID, resourceMap, v1.KubernetesExclusionConfig{})
			extractor.indexObjects([]*unstructured.Unstructured{canary, role, binding})
			extractor.processRole(role)
			extractor.processRoleBinding(binding)

			access := extractor.getAccess()
			Expect(access).To(HaveLen(1))
			Expect(access[0].ConfigExternalID.ConfigType).To(Equal(ConfigTypePrefix + "Canary"))
			Expect(access[0].ConfigExternalID.ExternalID).To(Equal(KubernetesAlias(clusterName, "Canary", "default", "my-canary")))

			expectedRoleAlias := KubernetesAlias(clusterName, "ClusterRole", "", "canary-admin")
			Expect(access[0].ExternalRoleAliases).To(Equal([]string{expectedRoleAlias}))
			Expect(access[0].ID).ToNot(BeEmpty())
		})
	})
})

// Helper types and functions

func testRBACExtractor(clusterName string, scraperID *uuid.UUID) *rbacExtractor {
	resourceMap := make(map[string]string, len(builtinResourceKinds))
	for k, v := range builtinResourceKinds {
		resourceMap[k] = v
	}
	return newRBACExtractorWithResourceMap(clusterName, scraperID, resourceMap, v1.KubernetesExclusionConfig{})
}

func testRBACExtractorWithExclusions(clusterName string, scraperID *uuid.UUID, exclusions v1.KubernetesExclusionConfig) *rbacExtractor {
	resourceMap := make(map[string]string, len(builtinResourceKinds))
	for k, v := range builtinResourceKinds {
		resourceMap[k] = v
	}
	return newRBACExtractorWithResourceMap(clusterName, scraperID, resourceMap, exclusions)
}

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

func makeCustomResource(kind, name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "flanksource.com/v1",
			"kind":       kind,
			"metadata": map[string]any{
				"uid":               uuid.NewString(),
				"name":              name,
				"namespace":         namespace,
				"creationTimestamp": time.Now().Format(time.RFC3339),
			},
		},
	}
}
