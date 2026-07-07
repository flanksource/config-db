package v1

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("AWS", func() {
	DescribeTable("CloudTrail SourceType",
		func(config CloudTrail, want string) {
			Expect(config.SourceType()).To(Equal(want))
		},
		Entry("empty source without S3 uses api", CloudTrail{}, "api"),
		Entry("empty source with S3 bucket uses s3", CloudTrail{S3: &CloudTrailS3{Bucket: "logs"}}, "s3"),
		Entry("explicit api wins over S3 bucket", CloudTrail{Source: "api", S3: &CloudTrailS3{Bucket: "logs"}}, "api"),
		Entry("explicit s3", CloudTrail{Source: "s3"}, "s3"),
		Entry("unknown source falls back to api", CloudTrail{Source: "lake"}, "api"),
	)

	DescribeTable("CloudTrail GetMaxAge",
		func(config CloudTrail, want time.Duration) {
			Expect(config.GetMaxAge()).To(Equal(want))
		},
		Entry("api default", CloudTrail{}, 7*24*time.Hour),
		Entry("s3 default", CloudTrail{Source: "s3"}, 14*24*time.Hour),
		Entry("implicit s3 default", CloudTrail{S3: &CloudTrailS3{Bucket: "logs"}}, 14*24*time.Hour),
		Entry("explicit overrides api", CloudTrail{MaxAge: "30m"}, 30*time.Minute),
		Entry("explicit overrides s3", CloudTrail{Source: "s3", MaxAge: "1h"}, time.Hour),
	)

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
