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
		fp1, err1 := changes.Fingerprint(readChange("change_1.json"))
		fp3, err3 := changes.Fingerprint(readChange("change_3.json"))
		Expect(err1).ToNot(HaveOccurred())
		Expect(err3).ToNot(HaveOccurred())
		Expect(fp1).To(Equal(fp3))
	})

	It("Should calculate the same fingerprints for pod start", func() {
		fp2, err2 := changes.Fingerprint(readChange("change_2.json"))
		fp4, err4 := changes.Fingerprint(readChange("change_4.json"))
		Expect(err2).ToNot(HaveOccurred())
		Expect(err4).ToNot(HaveOccurred())
		Expect(fp2).To(Equal(fp4))
	})

	It("Should calculate the diff fingerprints for pod start", func() {
		fp1, err1 := changes.Fingerprint(readChange("change_1.json"))
		fp2, err2 := changes.Fingerprint(readChange("change_2.json"))
		Expect(err1).ToNot(HaveOccurred())
		Expect(err2).ToNot(HaveOccurred())
		Expect(fp1).ToNot(Equal(fp2))
	})
})

func TestChangeFingerprints(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Change Fingerprints Suite")
}

func TestFingerptingPlayground(t *testing.T) {
	val := `{
    "reason": "GitOperationFailed",
    "source": {
        "component": "source-controller"
    },
    "message": "failed to checkout and determine revision: unable to list remote for 'ssh://git@github.com/flanksource/aws-sandbox.git': ssh: handshake failed: knownhosts: key mismatch",
    "metadata": {
        "uid": "37c57cb4-8148-41bb-ae94-42c104b46e38",
        "name": "aws-sandbox.17e5b206b0f6d6f1",
        "namespace": "flux-system",
        "resourceVersion": "300464456",
        "creationTimestamp": "2024-08-15T09:44:51Z"
    },
    "involvedObject": {
        "uid": "962f999c-a9bd-40a4-80bf-47c84b1ad750",
        "kind": "GitRepository",
        "name": "aws-sandbox",
        "namespace": "flux-system",
        "apiVersion": "source.toolkit.fluxcd.io/v1",
        "resourceVersion": "300420822"
    }
}`

	ch := models.ConfigChange{}
	err := json.Unmarshal([]byte(val), &ch.Details)
	if err != nil {
		t.Fatal()
	}
	fp, err := changes.Fingerprint(&ch)
	if err != nil {
		t.Fail()
	}

	if fp != "fe3308b7187b2370ad8e16bd8cd5bad9" {
		t.Fail()
	}
}
