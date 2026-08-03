package gcp

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/structpb"

	v1 "github.com/flanksource/config-db/api/v1"
)

func sqlInstanceResult(name, selfLink, project string) v1.ScrapeResult {
	data, err := structpb.NewStruct(map[string]any{"selfLink": selfLink})
	Expect(err).ToNot(HaveOccurred())

	result := v1.ScrapeResult{
		Type:   v1.CloudSQLInstance,
		ID:     selfLink,
		Name:   name,
		Config: data,
	}
	if project != "" {
		result.Tags = map[string]string{"project": project}
	}
	return result
}

var _ = Describe("collectSQLInstances", func() {
	It("attributes each instance to the project that owns it", func() {
		instances := collectSQLInstances(v1.ScrapeResults{
			sqlInstanceResult("db-a", "https://sqladmin/instances/db-a", "gcp-proj-1"),
			sqlInstanceResult("db-b", "https://sqladmin/instances/db-b", "gcp-proj-2"),
			{Type: v1.GCSBucket, Name: "not-a-database"},
		}, "")

		Expect(instances).To(ConsistOf(
			instanceInfo{name: "db-a", selfLink: "https://sqladmin/instances/db-a", project: "gcp-proj-1"},
			instanceInfo{name: "db-b", selfLink: "https://sqladmin/instances/db-b", project: "gcp-proj-2"},
		))
	})

	It("falls back to the configured project when the tag is missing", func() {
		instances := collectSQLInstances(v1.ScrapeResults{
			sqlInstanceResult("db-a", "https://sqladmin/instances/db-a", ""),
		}, "gcp-proj-fallback")

		Expect(instances).To(HaveLen(1))
		Expect(instances[0].project).To(Equal("gcp-proj-fallback"))
	})

	It("uses the result id when the config carries no self link", func() {
		instances := collectSQLInstances(v1.ScrapeResults{
			{Type: v1.CloudSQLInstance, ID: "instance-id", Name: "db-a"},
		}, "gcp-proj-1")

		Expect(instances).To(HaveLen(1))
		Expect(instances[0].selfLink).To(Equal("instance-id"))
	})
})

var _ = Describe("instancesByProject", func() {
	It("groups instances so each project is listed once", func() {
		grouped := instancesByProject([]instanceInfo{
			{name: "db-a", project: "gcp-proj-1"},
			{name: "db-b", project: "gcp-proj-2"},
			{name: "db-c", project: "gcp-proj-1"},
		})

		Expect(grouped).To(HaveLen(2))
		Expect(grouped["gcp-proj-1"]).To(HaveLen(2))
		Expect(grouped["gcp-proj-2"]).To(HaveLen(1))
	})

	It("drops instances with no resolvable project rather than querying an empty one", func() {
		grouped := instancesByProject([]instanceInfo{
			{name: "db-a", project: ""},
			{name: "db-b", project: "gcp-proj-1"},
		})

		Expect(grouped).To(HaveLen(1))
		Expect(grouped).To(HaveKey("gcp-proj-1"))
	})
})
