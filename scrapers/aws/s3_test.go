package aws

import (
	"fmt"

	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/samber/lo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("S3 bucket config", func() {
	It("embeds a bucket policy as structured JSON", func() {
		policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Action":"s3:*","Resource":"*"}]}`

		config, err := s3BucketConfig(s3Types.Bucket{Name: lo.ToPtr("audit-logs")}, &policy)

		Expect(err).NotTo(HaveOccurred())
		Expect(config).To(HaveKeyWithValue("Name", "audit-logs"))
		document, ok := config["Policy"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(document).To(HaveKeyWithValue("Version", "2012-10-17"))
		Expect(document["Statement"]).To(HaveLen(1))
	})

	It("omits Policy when the bucket has no policy", func() {
		config, err := s3BucketConfig(s3Types.Bucket{Name: lo.ToPtr("unmanaged")}, nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(config).NotTo(HaveKey("Policy"))
	})

	It("recognizes S3's untyped missing-policy error", func() {
		missing := fmt.Errorf("get policy: %w", &smithy.GenericAPIError{Code: "NoSuchBucketPolicy"})
		denied := &smithy.GenericAPIError{Code: "AccessDenied"}

		Expect(isNoSuchBucketPolicy(missing)).To(BeTrue())
		Expect(isNoSuchBucketPolicy(denied)).To(BeFalse())
	})
})
