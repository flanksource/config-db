package github

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	v1 "github.com/flanksource/config-db/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GitHub Scraper E2E", func() {
	It("should scrape a repository via the CLI binary", func() {
		if os.Getenv("GITHUB_TOKEN") == "" {
			Skip("GITHUB_TOKEN not set, skipping e2e test")
		}

		rootDir := findRootDir()
		binary := filepath.Join(GinkgoT().TempDir(), "config-db")

		build := exec.Command("go", "build", "-o", binary, ".")
		build.Dir = rootDir
		out, err := build.CombinedOutput()
		Expect(err).ToNot(HaveOccurred(), "failed to build: %s", string(out))

		outputDir := GinkgoT().TempDir()
		fixture := filepath.Join(rootDir, "fixtures", "github.yaml")

		run := exec.Command(binary, "run", fixture, "-o", outputDir, "--debug-port", "-1")
		run.Env = append(os.Environ(), "GITHUB_TOKEN="+os.Getenv("GITHUB_TOKEN"))
		runOut, err := run.CombinedOutput()
		if err != nil {
			exitCode := -1
			if run.ProcessState != nil {
				exitCode = run.ProcessState.ExitCode()
			}
			Expect(err).ToNot(HaveOccurred(), "config-db run failed (exit %v):\n%s", exitCode, string(runOut))
		}

		repoFile := findOutputFile(outputDir, "canary-checker")
		data, err := os.ReadFile(repoFile)
		Expect(err).ToNot(HaveOccurred())

		var result v1.ScrapeResult
		Expect(json.Unmarshal(data, &result)).To(Succeed())

		Expect(result.Type).To(Equal("GitHub::Repository"))
		Expect(result.Name).To(Equal("flanksource/canary-checker"))
		Expect(result.ConfigClass).To(Equal("Repository"))
		Expect(result.Config).ToNot(BeNil())
	})
})

func findRootDir() string {
	dir, err := os.Getwd()
	Expect(err).ToNot(HaveOccurred())
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		Expect(parent).ToNot(Equal(dir), "could not find project root (go.mod)")
		dir = parent
	}
}

func findOutputFile(dir, nameSubstr string) string {
	var matches []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() && strings.Contains(path, nameSubstr) && strings.HasSuffix(path, ".json") {
			matches = append(matches, path)
		}
		return nil
	})
	Expect(matches).ToNot(BeEmpty(), "no output file containing %q found in %s, contents: %s", nameSubstr, dir, listDir(dir))
	return matches[0]
}

func listDir(dir string) string {
	var files []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, _ error) error {
		rel, _ := filepath.Rel(dir, path)
		files = append(files, rel)
		return nil
	})
	data, _ := json.Marshal(files)
	return string(data)
}
