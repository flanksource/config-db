package git

import (
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Git Scraper Integration", ginkgo.Ordered, func() {
	var results v1.ScrapeResults

	ginkgo.It("should scrape a public git repository", func() {
		scrapeConfig := getConfigSpec("git")
		scraperCtx := api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		scraper := Scraper{}
		results = scraper.Scrape(scraperCtx)

		Expect(results).ToNot(BeEmpty(), "should return at least one result")
		for _, r := range results {
			if r.Error != nil {
				ginkgo.Fail("unexpected scrape error: " + r.Error.Error())
			}
		}
	})

	ginkgo.It("should have a Git::Repository config item with tags", func() {
		var repo *v1.ScrapeResult
		for i := range results {
			if results[i].Type == "Git::Repository" {
				repo = &results[i]
				break
			}
		}
		Expect(repo).ToNot(BeNil(), "should have a Git::Repository result")
		Expect(repo.ID).To(ContainSubstring("git/"))
		Expect(repo.ConfigClass).To(Equal("Repository"))
		Expect(repo.Name).ToNot(BeEmpty())
		Expect(repo.Tags).To(HaveKey("url"))
		Expect(repo.Tags).To(HaveKey("defaultBranch"))

		configMap, ok := repo.Config.(map[string]any)
		Expect(ok).To(BeTrue(), "repo config should be a map")
		Expect(configMap).To(HaveKey("url"))
		Expect(configMap).To(HaveKey("headSHA"))
		Expect(configMap).To(HaveKey("files"))

		files, ok := configMap["files"].([]FileEntry)
		Expect(ok).To(BeTrue(), "files should be a []FileEntry")
		Expect(files).ToNot(BeEmpty(), "file tree should not be empty")

		for _, f := range files {
			Expect(f.Name).ToNot(BeEmpty())
			Expect(f.SHA1).ToNot(BeEmpty())
			Expect(f.LastModifiedBy).ToNot(BeEmpty())
		}
	})

	ginkgo.It("should have a Git::Branch config item with tags as child of the repo", func() {
		var branch *v1.ScrapeResult
		for i := range results {
			if results[i].Type == "Git::Branch" {
				branch = &results[i]
				break
			}
		}
		Expect(branch).ToNot(BeNil(), "should have a Git::Branch result")
		Expect(branch.ID).To(ContainSubstring("::main"))
		Expect(branch.ConfigClass).To(Equal("Branch"))
		Expect(branch.Name).To(Equal("main"))
		Expect(branch.Tags).To(HaveKey("branch"))
		Expect(branch.Tags).To(HaveKey("url"))

		Expect(branch.Parents).To(HaveLen(1), "branch should have exactly one parent")
		Expect(branch.Parents[0].Type).To(Equal("Git::Repository"))

		configMap, ok := branch.Config.(map[string]any)
		Expect(ok).To(BeTrue(), "branch config should be a map")
		Expect(configMap).To(HaveKey("headSHA"))
		Expect(configMap).To(HaveKey("files"))
	})

	ginkgo.It("should produce commit changes with author and committer", func() {
		var branch *v1.ScrapeResult
		for i := range results {
			if results[i].Type == "Git::Branch" {
				branch = &results[i]
				break
			}
		}
		Expect(branch).ToNot(BeNil())
		Expect(branch.Changes).ToNot(BeEmpty(), "branch should have commit changes")

		for _, c := range branch.Changes {
			if c.ChangeType == "Commit" {
				Expect(c.ExternalChangeID).ToNot(BeEmpty(), "commit should have SHA")
				Expect(c.Summary).ToNot(BeEmpty(), "commit should have a summary")
				Expect(c.Source).To(Equal("git"))
				Expect(c.CreatedAt).ToNot(BeNil())
				Expect(c.CreatedBy).ToNot(BeNil())
				Expect(*c.CreatedBy).To(ContainSubstring("<"), "created_by should be in 'Name <email>' format")
				Expect(c.Details).To(HaveKey("author"))
				Expect(c.Details).To(HaveKey("committer"))
			}
		}
	})

	ginkgo.It("should respect file strategy rules", func() {
		var repo *v1.ScrapeResult
		for i := range results {
			if results[i].Type == "Git::Repository" {
				repo = &results[i]
				break
			}
		}
		Expect(repo).ToNot(BeNil())

		configMap := repo.Config.(map[string]any)
		files := configMap["files"].([]FileEntry)

		for _, f := range files {
			Expect(f.Name).ToNot(HavePrefix("vendor/"), "vendor files should be excluded by ignore strategy")
		}
	})

	ginkgo.It("should include diffs for .go files in commit changes", func() {
		var branch *v1.ScrapeResult
		for i := range results {
			if results[i].Type == "Git::Branch" {
				branch = &results[i]
				break
			}
		}
		Expect(branch).ToNot(BeNil())

		hasDiff := false
		for _, c := range branch.Changes {
			if c.ChangeType == "Commit" && c.Diff != nil && *c.Diff != "" {
				hasDiff = true
				break
			}
		}
		Expect(hasDiff).To(BeTrue(), "at least one commit should have diff content for .go files")
	})
})
