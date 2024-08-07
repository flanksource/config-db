package changes_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/flanksource/config-db/changes"
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
		fp1, err1 := changes.Fingerprint(readChange("change_1.json").Patches)
		fp3, err3 := changes.Fingerprint(readChange("change_3.json").Patches)
		Expect(err1).ToNot(HaveOccurred())
		Expect(err3).ToNot(HaveOccurred())
		Expect(fp1).To(Equal(fp3))
	})

	It("Should calculate the same fingerprints for pod start", func() {
		fp2, err2 := changes.Fingerprint(readChange("change_2.json").Patches)
		fp4, err4 := changes.Fingerprint(readChange("change_4.json").Patches)
		Expect(err2).ToNot(HaveOccurred())
		Expect(err4).ToNot(HaveOccurred())
		Expect(fp2).To(Equal(fp4))
	})

	It("Should calculate the diff fingerprints for pod start", func() {
		fp1, err1 := changes.Fingerprint(readChange("change_1.json").Patches)
		fp2, err2 := changes.Fingerprint(readChange("change_2.json").Patches)
		Expect(err1).ToNot(HaveOccurred())
		Expect(err2).ToNot(HaveOccurred())
		Expect(fp1).ToNot(Equal(fp2))
	})
})

func TestChangeFingerprints(t *testing.T) {

	RegisterFailHandler(Fail)
	RunSpecs(t, "Change Fingerprints Suite")
}
