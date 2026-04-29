package devops

import (
	"encoding/base64"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DecodeDescriptor", func() {
	DescribeTable("decodes every descriptor form to its canonical primitive",
		func(descriptor, projectName string, want DecodedDescriptor) {
			got := DecodeDescriptor(descriptor, projectName)
			Expect(got.Form).To(Equal(want.Form), "form")
			Expect(got.SID).To(Equal(want.SID), "sid")
			Expect(got.AADObjectID).To(Equal(want.AADObjectID), "aad object id")
			Expect(got.Email).To(Equal(want.Email), "email")
			Expect(got.ServiceLabel).To(Equal(want.ServiceLabel), "service label")
			Expect(got.ServicePayload).To(Equal(want.ServicePayload), "service payload")
		},

		Entry("legacy TF Identity → SID",
			"Microsoft.TeamFoundation.Identity;S-1-9-1551374245-1014925456-3005114181-2982025735-4262579767-1-2050131050-685675598-2688139908-3827195801",
			"",
			DecodedDescriptor{
				Form: "tfidentity",
				SID:  "S-1-9-1551374245-1014925456-3005114181-2982025735-4262579767-1-2050131050-685675598-2688139908-3827195801",
			}),

		Entry("vssgp Graph form → SID",
			"vssgp."+base64.RawStdEncoding.EncodeToString([]byte("S-1-9-1551374245-1014925456-3005114181-2982025735-4262579767-1-2050131050-685675598-2688139908-3827195801")),
			"",
			DecodedDescriptor{
				Form: "tfidentity",
				SID:  "S-1-9-1551374245-1014925456-3005114181-2982025735-4262579767-1-2050131050-685675598-2688139908-3827195801",
			}),

		Entry("aadgp Graph form → SID",
			"aadgp."+base64.RawStdEncoding.EncodeToString([]byte("S-1-9-1551374245-1204400969-2402986413-2179408616-3-726540237-2873640003-2286580144-3376613820")),
			"",
			DecodedDescriptor{
				Form: "aad-group",
				SID:  "S-1-9-1551374245-1204400969-2402986413-2179408616-3-726540237-2873640003-2286580144-3376613820",
			}),

		Entry("aad. user descriptor → AAD object id GUID",
			"aad."+base64.RawStdEncoding.EncodeToString([]byte("e9d284f3-c2f5-7270-bf70-8d872dee28ae")),
			"",
			DecodedDescriptor{
				Form:        "aad-user",
				AADObjectID: "e9d284f3-c2f5-7270-bf70-8d872dee28ae",
			}),

		Entry("ClaimsIdentity descriptor → email",
			`Microsoft.IdentityModel.Claims.ClaimsIdentity;00691924-e082-4301-a3dc-1732afd14289\MImmerman@oldmutual.com`,
			"",
			DecodedDescriptor{
				Form:  "claims",
				Email: "MImmerman@oldmutual.com",
			}),

		Entry("TF ServiceIdentity descriptor → labelled service + payload",
			"Microsoft.TeamFoundation.ServiceIdentity;17dc0655-2bd1-4b8a-aa5b-dd37c7982ae0:Build:8c6b3eb1-e5be-4309-b036-f7186f42623f",
			"OIPA",
			DecodedDescriptor{
				Form:           "service",
				ServiceLabel:   "Build Service (OIPA)",
				ServicePayload: "17dc0655-2bd1-4b8a-aa5b-dd37c7982ae0:Build:8c6b3eb1-e5be-4309-b036-f7186f42623f",
			}),

		Entry("svc. service principal descriptor → labelled service + same payload",
			"svc."+base64.RawStdEncoding.EncodeToString([]byte("17dc0655-2bd1-4b8a-aa5b-dd37c7982ae0:Build:8c6b3eb1-e5be-4309-b036-f7186f42623f")),
			"OIPA",
			DecodedDescriptor{
				Form:           "service-principal",
				ServiceLabel:   "Build Service (OIPA)",
				ServicePayload: "17dc0655-2bd1-4b8a-aa5b-dd37c7982ae0:Build:8c6b3eb1-e5be-4309-b036-f7186f42623f",
			}),

		Entry("empty input → unknown",
			"", "",
			DecodedDescriptor{Form: "unknown"}),
	)
})

var _ = Describe("CanonicalIdentityValue", func() {
	DescribeTable("returns the most useful value for cross-system matching",
		func(descriptor, projectName, want string) {
			Expect(CanonicalIdentityValue(descriptor, projectName)).To(Equal(want))
		},
		Entry("ClaimsIdentity → email",
			`Microsoft.IdentityModel.Claims.ClaimsIdentity;00691924-e082-4301-a3dc-1732afd14289\MImmerman@oldmutual.com`,
			"",
			"MImmerman@oldmutual.com"),
		Entry("aad. → AAD object id",
			"aad."+base64.RawStdEncoding.EncodeToString([]byte("e9d284f3-c2f5-7270-bf70-8d872dee28ae")),
			"",
			"e9d284f3-c2f5-7270-bf70-8d872dee28ae"),
		Entry("vssgp → SID",
			"vssgp."+base64.RawStdEncoding.EncodeToString([]byte("S-1-9-1551374245-1")),
			"",
			"S-1-9-1551374245-1"),
		Entry("ServiceIdentity → inner payload (the alias both forms share)",
			"Microsoft.TeamFoundation.ServiceIdentity;17dc0655-2bd1-4b8a-aa5b-dd37c7982ae0:Build:8c6b3eb1-e5be-4309-b036-f7186f42623f",
			"OIPA",
			"17dc0655-2bd1-4b8a-aa5b-dd37c7982ae0:Build:8c6b3eb1-e5be-4309-b036-f7186f42623f"),
	)
})

var _ = Describe("DescriptorAliases", func() {
	It("a ClaimsIdentity descriptor reduces to just the email", func() {
		descriptor := `Microsoft.IdentityModel.Claims.ClaimsIdentity;00691924-e082-4301-a3dc-1732afd14289\MImmerman@oldmutual.com`
		Expect(DescriptorAliases(descriptor)).To(Equal([]string{"MImmerman@oldmutual.com"}))
	})

	It("an aad. descriptor reduces to just the AAD object id GUID", func() {
		descriptor := "aad." + base64.RawStdEncoding.EncodeToString([]byte("e9d284f3-c2f5-7270-bf70-8d872dee28ae"))
		Expect(DescriptorAliases(descriptor)).To(Equal([]string{"e9d284f3-c2f5-7270-bf70-8d872dee28ae"}))
	})

	It("a vssgp descriptor reduces to just the SID — no descriptor synonyms", func() {
		sid := "S-1-9-1551374245-1014925456-3005114181-2982025735-4262579767-1-2050131050-685675598-2688139908-3827195801"
		descriptor := "vssgp." + base64.RawStdEncoding.EncodeToString([]byte(sid))
		Expect(DescriptorAliases(descriptor)).To(Equal([]string{sid}))
	})

	It("an unknown form returns the descriptor itself (only fallback)", func() {
		Expect(DescriptorAliases("cuckoo.something")).To(Equal([]string{"cuckoo.something"}))
	})

	It("ServiceIdentity and svc. forms reduce to the same inner payload — that is the merge alias", func() {
		payload := "17dc0655-2bd1-4b8a-aa5b-dd37c7982ae0:Build:8c6b3eb1-e5be-4309-b036-f7186f42623f"
		legacy := "Microsoft.TeamFoundation.ServiceIdentity;" + payload
		graph := "svc." + base64.RawStdEncoding.EncodeToString([]byte(payload))

		// Both forms emit the inner payload plus the service label (here the
		// label uses the project GUID since DescriptorAliases doesn't have a
		// project name to substitute — both forms still produce the same set
		// of aliases, which is what the merge needs).
		legacyAliases := DescriptorAliases(legacy)
		graphAliases := DescriptorAliases(graph)
		Expect(legacyAliases).To(Equal(graphAliases))
		Expect(legacyAliases).To(ContainElement(payload))
	})
})

var _ = Describe("ResolvedIdentityName", func() {
	It("uses the projectName label for ServiceIdentity descriptors", func() {
		id := ResolvedIdentity{
			Descriptor:          "Microsoft.TeamFoundation.ServiceIdentity;17dc0655-2bd1-4b8a-aa5b-dd37c7982ae0:Build:8c6b3eb1-e5be-4309-b036-f7186f42623f",
			ProviderDisplayName: "8c6b3eb1-e5be-4309-b036-f7186f42623f",
		}
		Expect(ResolvedIdentityName(id, "OIPA")).To(Equal("Build Service (OIPA)"))
	})

	It("falls through to ProviderDisplayName for ClaimsIdentity (caller already has the email)", func() {
		id := ResolvedIdentity{
			Descriptor:          `Microsoft.IdentityModel.Claims.ClaimsIdentity;00691924-e082-4301-a3dc-1732afd14289\MImmerman@oldmutual.com`,
			ProviderDisplayName: "Moshe Alexander Immerman",
		}
		Expect(ResolvedIdentityName(id, "")).To(Equal("Moshe Alexander Immerman"))
	})
})

var _ = Describe("isValidDescriptor", func() {
	DescribeTable("rejects descriptors that would 400 on _apis/identities",
		func(descriptor string, want bool) {
			Expect(isValidDescriptor(descriptor)).To(Equal(want))
		},
		Entry("empty string", "", false),
		Entry("prefix-only Microsoft.TeamFoundation.Identity;", "Microsoft.TeamFoundation.Identity;", false),
		Entry("prefix-only ServiceIdentity;", "Microsoft.TeamFoundation.ServiceIdentity;", false),
		Entry("prefix-only ClaimsIdentity;", "Microsoft.IdentityModel.Claims.ClaimsIdentity;", false),
		Entry("prefix-only vssgp.", "vssgp.", false),
		Entry("prefix-only aadgp.", "aadgp.", false),
		Entry("prefix-only aad.", "aad.", false),
		Entry("prefix-only svc.", "svc.", false),
		Entry("s2s. is unresolvable; reject", "s2s.token", false),
		Entry("bare string with no recognised separator", "foo", false),
		Entry("legacy SID descriptor", "Microsoft.TeamFoundation.Identity;S-1-9-1234", true),
		Entry("legacy ServiceIdentity descriptor", "Microsoft.TeamFoundation.ServiceIdentity;owner:type:guid", true),
		Entry("ClaimsIdentity with email payload",
			`Microsoft.IdentityModel.Claims.ClaimsIdentity;abc\alice@example.com`, true),
		Entry("vssgp with payload", "vssgp.UyXX", true),
		Entry("aadgp with payload", "aadgp.UyXX", true),
		Entry("aad with payload", "aad.YjVh", true),
		Entry("svc with payload", "svc.aGVsbG8", true),
	)
})
