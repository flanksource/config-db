package file

import (
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/filesystem"
	"github.com/hashicorp/go-getter"
	"github.com/tidwall/gjson"
	"sigs.k8s.io/yaml"
)

// FileScrapper ...
type FileScrapper struct {
}

const charset = "abcdefghijklmnopqrstuvwxyz" +
	"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// Scrape ...
func (file FileScrapper) Scrape(ctx v1.ScrapeContext, config v1.ConfigScraper, manager v1.Manager) []v1.ScrapeResult {
	results := []v1.ScrapeResult{}
	finder := manager.Finder
	for _, fileConfig := range config.File {
		var globMatches []string
		if fileConfig.URL != "" {
			var tempDir string
			globMatches, tempDir = findFilesFromURL(fileConfig.URL, fileConfig.Paths, finder)
			defer os.RemoveAll(tempDir)
		} else {
			globMatches = findFiles("", fileConfig.Paths, finder)
		}
		for _, match := range globMatches {
			if !isYaml(match) && !isJson(match) {
				logger.Debugf("skipping file %s, not a yaml or json file", match)
				continue
			}
			contentByte, filename, err := finder.Read(match)
			if err != nil {
				logger.Errorf("failed to reading matched file: %v", err)
				continue
			}
			var jsonContent string
			if isYaml(match) {
				contentByte, err := yaml.YAMLToJSON(contentByte)
				if err != nil {
					logger.Errorf("failed to convert yaml to json: %v", err)
					continue
				}
				jsonContent = string(contentByte)
			} else {
				jsonContent = string(contentByte)
			}
			resultID := gjson.Get(jsonContent, fileConfig.ID).String()
			resultType := gjson.Get(jsonContent, fileConfig.Type).String()
			if resultID == "" {
				logger.Errorf("failed to the specified gjson path: %s in file %s", fileConfig.ID, filename)
				continue
			}
			results = append(results, v1.ScrapeResult{
				Config: jsonContent,
				Type:   resultType,
				ID:     resultID,
			})

		}

	}
	return results
}

func findFilesFromURL(url string, paths []string, finder filesystem.Finder) (matches []string, dirname string) {
	tempDir := GetTempDirName(10, charset)
	if err := getter.GetAny(tempDir, url); err != nil {
		logger.Errorf("Error downloading file: %s", err)
	}
	return findFiles(tempDir, paths, finder), tempDir
}

func findFiles(dir string, paths []string, finder filesystem.Finder) []string {
	matches := []string{}
	if paths == nil {
		logger.Debugf("no paths specified, scrapping all json and yaml/yml files")
		paths = append(paths, "**.json", "**.yaml", "**.yml")
	}
	for _, path := range paths {
		match, err := finder.Find(filepath.Join(dir, path))
		if err != nil {
			logger.Tracef("could not match glob pattern(%s): %v", dir+"/"+path, err)
			continue
		}
		matches = append(matches, match...) // using a seperate slice to avoid nested loops and complexity
	}
	return matches
}

func isYaml(filename string) bool {
	return filepath.Ext(filename) == ".yaml" || filepath.Ext(filename) == ".yml"
}

func isJson(filename string) bool {
	return filepath.Ext(filename) == ".json"
}

var seededRand *rand.Rand = rand.New(
	rand.NewSource(time.Now().UnixNano()))

func GetTempDirName(length int, charset string) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[seededRand.Intn(len(charset))]
	}
	return string(b)
}
