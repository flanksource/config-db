package v1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("AWSExclusions.Matches", func() {
	DescribeTable("rules",
		func(rules AWSExclusions, cfgType, name string, tags map[string]string, want bool) {
			Expect(rules.Matches(cfgType, name, tags)).To(Equal(want))
		},
		Entry("type-only exact match",
			AWSExclusions{{Type: AWSIAMRole}},
			AWSIAMRole, "ams-admin", nil, true),
		Entry("type mismatch",
			AWSExclusions{{Type: AWSIAMRole}},
			AWSS3Bucket, "ams-admin", nil, false),
		Entry("name glob prefix",
			AWSExclusions{{Name: "ams*"}},
			AWSIAMRole, "ams-admin", nil, true),
		Entry("name glob no match",
			AWSExclusions{{Name: "ams*"}},
			AWSIAMRole, "prod-admin", nil, false),
		Entry("name negation drops matching items from exclusion",
			AWSExclusions{{Name: "!ams*"}},
			AWSIAMRole, "ams-admin", nil, false),
		Entry("name negation excludes non-matching",
			AWSExclusions{{Name: "!ams*"}},
			AWSIAMRole, "prod-admin", nil, true),
		Entry("AND type+name both match",
			AWSExclusions{{Type: AWSIAMRole, Name: "ams*"}},
			AWSIAMRole, "ams-x", nil, true),
		Entry("AND type+name, only name matches — rule does not fire",
			AWSExclusions{{Type: AWSIAMRole, Name: "ams*"}},
			AWSS3Bucket, "ams-x", nil, false),
		Entry("AND type+name, only type matches — rule does not fire",
			AWSExclusions{{Type: AWSIAMRole, Name: "ams*"}},
			AWSIAMRole, "prod-x", nil, false),
		Entry("tag value glob match",
			AWSExclusions{{Tags: map[string]string{"Environment": "ephemeral*"}}},
			AWSEC2Instance, "i-1", map[string]string{"Environment": "ephemeral-123"}, true),
		Entry("tag key missing on item — rule does not fire",
			AWSExclusions{{Tags: map[string]string{"Environment": "*"}}},
			AWSEC2Instance, "i-1", map[string]string{"Owner": "team-a"}, false),
		Entry("tag rule with nil item tags — rule does not fire (IAM case)",
			AWSExclusions{{Tags: map[string]string{"Environment": "*"}}},
			AWSIAMRole, "ams-x", nil, false),
		Entry("AND type+tag both match",
			AWSExclusions{{Type: AWSEC2Instance, Tags: map[string]string{"Environment": "ephemeral*"}}},
			AWSEC2Instance, "i-1", map[string]string{"Environment": "ephemeral-1"}, true),
		Entry("OR across rules — first rule fires",
			AWSExclusions{{Name: "ams*"}, {Type: AWSS3Bucket}},
			AWSS3Bucket, "ams-x", nil, true),
		Entry("OR across rules — second rule fires",
			AWSExclusions{{Name: "ams*"}, {Type: AWSS3Bucket}},
			AWSS3Bucket, "prod-bucket", nil, true),
		Entry("OR across rules — neither fires",
			AWSExclusions{{Name: "ams*"}, {Type: AWSS3Bucket}},
			AWSIAMRole, "prod-admin", nil, false),
		Entry("empty rule is inert",
			AWSExclusions{{}},
			AWSIAMRole, "anything", nil, false),
		Entry("empty exclusions list",
			AWSExclusions{},
			AWSIAMRole, "ams-x", nil, false),

		// Comma-separated name patterns — OR-combined globs within a single string.
		Entry("comma-separated name — first pattern matches",
			AWSExclusions{{Name: "ams*,AWSServiceRole*"}},
			AWSIAMRole, "ams-billing", nil, true),
		Entry("comma-separated name — second pattern matches",
			AWSExclusions{{Name: "ams*,AWSServiceRole*"}},
			AWSIAMRole, "AWSServiceRoleForAutoScaling", nil, true),
		Entry("comma-separated name — neither pattern matches",
			AWSExclusions{{Name: "ams*,AWSServiceRole*"}},
			AWSIAMRole, "my-app-role", nil, false),
		Entry("comma-separated name tolerates surrounding whitespace",
			AWSExclusions{{Name: " ams* , AWSServiceRole* "}},
			AWSIAMRole, "AWSServiceRoleForAutoScaling", nil, true),
		Entry("comma-separated name with negation — negation wins",
			AWSExclusions{{Name: "!critical-*,*"}},
			AWSIAMRole, "critical-admin", nil, false),
		Entry("comma-separated name with negation — non-critical matches catch-all",
			AWSExclusions{{Name: "!critical-*,*"}},
			AWSIAMRole, "other-role", nil, true),

		// Comma-separated Type.
		Entry("comma-separated type — matches second type",
			AWSExclusions{{Type: "AWS::IAM::Role,AWS::CloudFormation::Stack", Name: "ams*"}},
			AWSCloudFormationStack, "ams-core", nil, true),
	)
})

var _ = Describe("splitPatterns", func() {
	DescribeTable("splits comma-separated, trimmed, non-empty",
		func(in string, want []string) {
			Expect(splitPatterns(in)).To(Equal(want))
		},
		Entry("empty string", "", nil),
		Entry("single pattern", "ams*", []string{"ams*"}),
		Entry("two patterns", "a,b", []string{"a", "b"}),
		Entry("whitespace around commas", " a , b ", []string{"a", "b"}),
		Entry("trailing empty piece dropped", "a,,b,", []string{"a", "b"}),
	)
})
