package changes

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/flanksource/config-db/db/models"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func readChange(name string) *models.ConfigChange {
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		Fail("failed to read test data file: " + err.Error())
	}
	var change models.ConfigChange
	if err := json.Unmarshal(data, &change); err != nil {
		Fail("failed to unmarshal change from testdata: " + err.Error())
	}
	return &change
}

var _ = Describe("Change Fingerprints", func() {

	It("Should calculate the same fingerprints for pod stop", func() {
		Expect(Fingerprint(readChange("change_1.json"))).To(Equal(Fingerprint(readChange("change_3.json"))))
	})
	It("Should calculate the same fingerprints for pod start", func() {
		Expect(Fingerprint(readChange("change_2.json"))).To(Equal(Fingerprint(readChange("change_4.json"))))
	})
	It("Should calculate the diff fingerprints for pod start", func() {
		Expect(Fingerprint(readChange("change_1.json"))).ToNot(Equal(Fingerprint(readChange("change_2.json"))))
	})
})

func TestChangeFingerprints(t *testing.T) {

	RegisterFailHandler(Fail)
	RunSpecs(t, "Change Fingerprints Suite")
}
