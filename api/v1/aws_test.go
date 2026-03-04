package v1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("AWS", func() {
	DescribeTable("Includes",
		func(config AWS, resource string, want bool) {
			Expect(config.Includes(resource)).To(Equal(want))
		},
		Entry("empty include list, not in default exclusions",
			AWS{}, "ec2", true),
		Entry("empty include list, in default exclusions",
			AWS{}, "ECSTASKDEFINITION", false),
		Entry("explicit inclusion of default exclusion",
			AWS{Include: []string{"EcsTaskDefinition"}}, "ECSTASKDEFINITION", true),
		Entry("non-empty include list, resource included",
			AWS{Include: []string{"s3", "ec2", "rds"}}, "ec2", true),
		Entry("non-empty include list, resource not included",
			AWS{Include: []string{"s3", "ec2", "rds"}}, "lambda", false),
		Entry("case-insensitive include",
			AWS{Include: []string{"S3", "EC2", "RDS"}}, "ec2", true),
	)
})
