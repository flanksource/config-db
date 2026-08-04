package gcp

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutyContext "github.com/flanksource/duty/context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/types/known/structpb"
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

var _ = Describe("Cloud SQL operation partial results", func() {
	It("keeps successful project changes when another project fails", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.Contains(r.URL.Path, "/projects/gcp-proj-1/operations"):
				_, _ = w.Write([]byte(`{"items":[{"name":"operation-a","operationType":"EXPORT","status":"DONE","targetId":"db-a","startTime":"2025-06-19T12:00:00Z"}]}`))
			case strings.Contains(r.URL.Path, "/projects/gcp-proj-2/operations"):
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"code":403,"message":"permission denied"}}`))
			default:
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"code":404,"message":"not found"}}`))
			}
		}))
		defer server.Close()

		ctx := &GCPContext{
			ScrapeContext: api.NewScrapeContext(dutyContext.New()),
			ClientOpts: []option.ClientOption{
				option.WithEndpoint(server.URL + "/"),
				option.WithoutAuthentication(),
			},
		}
		assets := v1.ScrapeResults{
			sqlInstanceResult("db-a", "https://sqladmin/instances/db-a", "gcp-proj-1"),
			sqlInstanceResult("db-b", "https://sqladmin/instances/db-b", "gcp-proj-2"),
		}

		results, err := (Scraper{}).scrapeCloudSQLBackupsForAllInstances(ctx, v1.GCP{
			Projects: []string{"gcp-proj-1", "gcp-proj-2"},
		}, "organizations/1234", assets)

		Expect(err).ToNot(HaveOccurred())
		var changes []v1.ChangeResult
		var errors int
		for _, result := range results {
			changes = append(changes, result.Changes...)
			if result.Error != nil {
				errors++
				Expect(result.Error.Error()).To(ContainSubstring("gcp-proj-2"))
			}
		}
		Expect(errors).To(Equal(1))
		Expect(changes).To(HaveLen(1))
		Expect(changes[0].ExternalChangeID).To(Equal("operation-a"))
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
