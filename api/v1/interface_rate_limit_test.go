package v1

import (
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("RateLimit", func() {
	It("should mark results as rate limited with reset time", func() {
		resetAt := time.Now().Add(10 * time.Minute)
		var results ScrapeResults
		results.RateLimited("test rate limit", &resetAt)

		Expect(results.IsRateLimited()).To(BeTrue())
		Expect(results.HasErr()).To(BeTrue())
		Expect(results.GetRateLimitResetAt()).ToNot(BeNil())
		Expect(results.GetRateLimitResetAt().Equal(resetAt)).To(BeTrue())
		Expect(errors.Is(results[0].Error, ErrRateLimited)).To(BeTrue())
	})

	It("should handle nil reset time", func() {
		var results ScrapeResults
		results.RateLimited("rate limit without reset", nil)

		Expect(results.IsRateLimited()).To(BeTrue())
		Expect(results.GetRateLimitResetAt()).To(BeNil())
	})

	It("should return false for normal results", func() {
		results := ScrapeResults{
			{ID: "test-1", Name: "normal"},
		}

		Expect(results.IsRateLimited()).To(BeFalse())
		Expect(results.GetRateLimitResetAt()).To(BeNil())
	})

	It("should return false for non-rate-limit errors", func() {
		results := ScrapeResults{
			{Error: errors.New("some other error")},
		}

		Expect(results.IsRateLimited()).To(BeFalse())
	})

	It("should append to existing results", func() {
		resetAt := time.Now().Add(5 * time.Minute)
		results := ScrapeResults{
			{ID: "existing-1", Name: "existing"},
		}
		results.RateLimited("hit limit", &resetAt)

		Expect(results).To(HaveLen(2))
		Expect(results.IsRateLimited()).To(BeTrue())
	})
})
