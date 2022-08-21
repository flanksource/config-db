package file

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/filesystem"
	"github.com/gobwas/glob"
	"github.com/hashicorp/go-getter"
	"sigs.k8s.io/yaml"
)

// FileScrapper ...
type FileScrapper struct {
}

const charset = "abcdefghijklmnopqrstuvwxyz" +
	"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

var seededRand *rand.Rand = rand.New(
	rand.NewSource(time.Now().UnixNano()))

func isIgnored(config v1.File, path string) (bool, error) {
	if !isYaml(path) && !isJson(path) {
		logger.Tracef("skipping file %s, not a yaml or json file", path)
		return true, nil
	}

	for _, ignore := range config.Ignore {
		g, err := glob.Compile(ignore)
		if err != nil {
			return false, err
		}
		if g.Match(path) {
			logger.Tracef("ignore %s matched %s", ignore, path)
			return true, nil
		}
	}
	return false, nil
}

// Scrape ...
func (file FileScrapper) Scrape(ctx v1.ScrapeContext, configs v1.ConfigScraper, manager v1.Manager) v1.ScrapeResults {
	results := v1.ScrapeResults{}
	finder := manager.Finder
	var tempDir string
	for _, config := range configs.File {
		var globMatches []string
		if config.URL != "" {
			globMatches, tempDir = findFilesFromURL(config.URL, config.Paths, finder)
			defer os.RemoveAll(tempDir)
		} else {
			globMatches = findFiles("", config.Paths, finder)
		}
		for _, match := range globMatches {
			file := strings.Replace(match, tempDir+"/", "", 1)
			var result = v1.ScrapeResult{
				BaseScraper: config.BaseScraper,
				Source:      config.RedactedString() + "/" + file,
			}
			if ignore, err := isIgnored(config, file); err != nil {
				results = append(results, result.Errorf("failed to check if file %s is ignored: %v", file, err))
				continue
			} else if ignore {
				continue
			}
			contentByte, _, err := finder.Read(match)
			if err != nil {
				results = append(results, result.Errorf("failed to read file %s: %v", file, err))
				continue
			}
			var jsonContent string
			if isYaml(match) {
				contentByte, err := yaml.YAMLToJSON(contentByte)
				if err != nil {
					results = append(results, result.Errorf("failed to convert yaml to json: %v", err))
					continue
				}
				jsonContent = string(contentByte)
			} else {
				jsonContent = string(contentByte)
			}
			results = append(results, result.Success(jsonContent))
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

func GetTempDirName(length int, charset string) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[seededRand.Intn(len(charset))]
	}
	return string(b)
}
