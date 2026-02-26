package github

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitHubScraperE2E(t *testing.T) {
	if os.Getenv("GITHUB_TOKEN") == "" {
		t.Skip("GITHUB_TOKEN not set, skipping e2e test")
	}

	rootDir := findRootDir(t)
	binary := filepath.Join(t.TempDir(), "config-db")

	t.Log("building config-db binary")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = rootDir
	out, err := build.CombinedOutput()
	require.NoError(t, err, "failed to build: %s", string(out))

	outputDir := t.TempDir()
	fixture := filepath.Join(rootDir, "fixtures", "github.yaml")

	t.Log("running config-db run")
	run := exec.Command(binary, "run", fixture, "-o", outputDir, "--debug-port", "-1")
	run.Env = append(os.Environ(), "GITHUB_TOKEN="+os.Getenv("GITHUB_TOKEN"))
	runOut, err := run.CombinedOutput()
	require.NoError(t, err, "config-db run failed (exit %v):\n%s", run.ProcessState.ExitCode(), string(runOut))

	repoFile := findOutputFile(t, outputDir, "canary-checker")
	data, err := os.ReadFile(repoFile)
	require.NoError(t, err)

	var result v1.ScrapeResult
	require.NoError(t, json.Unmarshal(data, &result))

	assert.Equal(t, "GitHub::Repository", result.Type)
	assert.Equal(t, "flanksource/canary-checker", result.Name)
	assert.Equal(t, "Repository", result.ConfigClass)
	assert.NotNil(t, result.Config)
}

func findRootDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root (go.mod)")
		}
		dir = parent
	}
}

func findOutputFile(t *testing.T, dir, nameSubstr string) string {
	t.Helper()
	var matches []string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() && strings.Contains(path, nameSubstr) && strings.HasSuffix(path, ".json") {
			matches = append(matches, path)
		}
		return nil
	})
	require.NotEmpty(t, matches, "no output file containing %q found in %s, contents: %s", nameSubstr, dir, listDir(dir))
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
