package utils

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type TestStruct struct {
	Name    string `json:"name,omitempty"`
	Age     int    `json:"age,omitempty"`
	Email   string `json:"email,omitempty"`
	Country string `json:"country,omitempty"`
}

var _ = Describe("ToJSONMap", func() {
	DescribeTable("converting structs to maps",
		func(input interface{}, expectedMap map[string]any, expectError bool) {
			resultMap, err := ToJSONMap(input)
			if expectError {
				Expect(err).To(HaveOccurred())
				Expect(resultMap).To(BeNil())
			} else {
				Expect(err).ToNot(HaveOccurred())
				Expect(resultMap).To(Equal(expectedMap))
			}
		},
		Entry("struct with string fields",
			TestStruct{Name: "John Doe", Age: 30, Email: "john.doe@example.com", Country: "USA"},
			map[string]any{"name": "John Doe", "age": float64(30), "email": "john.doe@example.com", "country": "USA"},
			false,
		),
		Entry("empty struct",
			TestStruct{},
			map[string]any{},
			false,
		),
		Entry("non-serializable input",
			make(chan int),
			nil,
			true,
		),
	)
})

var _ = Describe("ExtractLeafNodesAndCommonParents", func() {
	DescribeTable("extracting changed paths",
		func(data string, expectedPaths []string) {
			var m map[string]any
			Expect(json.Unmarshal([]byte(data), &m)).To(Succeed())
			paths := ExtractLeafNodesAndCommonParents(m)
			Expect(paths).To(ConsistOf(expectedPaths))
		},
		Entry("single child on all levels",
			`{"address": {"city": {"name": "Kathmandu"}}}`,
			[]string{"address.city.name"},
		),
		Entry("multiple children on some level",
			`{"address": {"city": {"name": "Imadol", "district": "Lalitpur"}}}`,
			[]string{"address.city"},
		),
		Entry("multiple children with nested block",
			`{"address": {"city": {"name": "Imadol", "district": "Lalitpur", "block": {"section": "b", "name": "Sagarmatha Tol"}}}}`,
			[]string{"address.city", "address.city.block"},
		),
		Entry("multiple top level children",
			`{"metadata": {"annotations": {"control-plane.alpha.kubernetes.io/leader": "{\"holderIdentity\":\"ip-172-16-56-162.eu-west-2.compute.internal\",\"leaseDurationSeconds\":30,\"acquireTime\":\"2023-03-07T10:13:03Z\",\"renewTime\":\"2023-03-16T05:10:21Z\",\"leaderTransitions\":25}"}, "resourceVersion": "483339358"}}`,
			[]string{"metadata.resourceVersion", "metadata.annotations.control-plane.alpha.kubernetes.io/leader"},
		),
		Entry("a child with an array",
			`{"status": {"conditions": [{"type": "Ready", "reason": "ChartPullFailed", "status": "False", "message": "no chart version found for mysql-8.8.8", "lastTransitionTime": "2023-03-16T04:47:24.000Z"}]}, "metadata": {"resourceVersion": "483324452"}}`,
			[]string{"status.conditions", "metadata.resourceVersion"},
		),
		Entry("deeply nested",
			`{"a": {"b": {"c": {"d": {"e": {"f": 1, "g": 2}}}, "h": 3, "i": {"j": {"k": 4}}}}, "metadata": {"resourceVersion": "483324452"}}`,
			[]string{"a.b.c.d.e", "a.b.h", "a.b.i.j.k", "metadata.resourceVersion"},
		),
	)
})
