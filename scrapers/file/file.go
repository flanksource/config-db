package file

import (
	"crypto/md5"
	"encoding/hex"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/duty"
	"github.com/gobwas/glob"
	"github.com/hashicorp/go-getter"
	"sigs.k8s.io/yaml"
)

// FileScraper ...
type FileScraper struct {
}

func isIgnored(config v1.File, path string) (bool, error) {
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

// stripSecrets returns the url with the password removed
func stripSecrets(uri string) string {
	_uri, _ := url.Parse(stripPrefix(uri))
	if _uri == nil {
		return uri
	}
	return _uri.Redacted()
}

func stripPrefix(filename string) string {
	filename = regexp.MustCompile(`^\w+::`).ReplaceAllString(filename, "")
	return strings.Replace(filename, "file://", "", 1)
}

// convert url into a local path supported on linux filesystems
func convertToLocalPath(uri string) string {
	_uri, err := url.Parse(stripPrefix(uri))
	if err != nil {
		return uri
	}
	hash := md5.Sum([]byte(uri))
	p := ""
	if _uri.Host != "" {
		p = _uri.Host + "-"
	}
	return p + path.Base(_uri.Path) + "-" + hex.EncodeToString(hash[:])[0:8]
}

func (file FileScraper) CanScrape(configs v1.ConfigScraper) bool {
	return len(configs.File) > 0
}

func (file FileScraper) Scrape(ctx *v1.ScrapeContext, configs v1.ConfigScraper) v1.ScrapeResults {
	pwd, _ := os.Getwd()
	cacheDir := path.Join(pwd, ".config-db", "cache", "files")
	results := v1.ScrapeResults{}

	for _, config := range configs.File {
		connection := config.GetConnection()

		if _connection, err := duty.FindConnectionFromConnectionString(ctx, db.DefaultDB(), connection.URL); err != nil {
			results.Errorf(err, "failed to find connection from (url=%s)", connection.URL)
			continue
		} else if _connection != nil {
			connection = _connection
		}

		url := stripSecrets(connection.URL)
		tempDir := path.Join(cacheDir, convertToLocalPath(url))
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			return results.Errorf(err, "failed to create cache dir: %v", tempDir)
		}

		logger.Debugf("Scraping file %s ==> %s", stripSecrets(connection.URL), tempDir)
		var globMatches []string
		if connection.URL != "" {
			globMatches = getFiles(ctx, tempDir, connection.URL, config.Paths)
		} else {
			globMatches = findFiles(ctx, "", config.Paths)
		}

		for _, match := range globMatches {
			file := strings.Replace(match, tempDir+"/", "", 1)
			var result = v1.NewScrapeResult(config.BaseScraper)
			if config.Format != "" {
				result.Format = config.Format
			}
			if config.Icon != "" {
				result.Icon = config.Icon
			}
			result.Source = config.RedactedString() + "/" + file

			if ignore, err := isIgnored(config, file); err != nil {
				results = append(results, result.Errorf("failed to check if file %s is ignored: %v", file, err))
				continue
			} else if ignore {
				continue
			}

			contentByte, _, err := ctx.Read(match)
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

func getFiles(ctx *v1.ScrapeContext, dst, url string, paths []string) (matches []string) {
	logger.Debugf("Downloading files from %s to %s", stripSecrets(url), dst)
	if err := getter.GetAny(dst, url); err != nil {
		logger.Errorf("Error downloading file: %s", err)
	}
	return findFiles(ctx, dst, paths)
}

func findFiles(ctx *v1.ScrapeContext, dir string, paths []string) []string {
	matches := []string{}
	if paths == nil {
		logger.Debugf("no paths specified, scrapping all json and yaml/yml files")
		paths = append(paths, "**.json", "**.yaml", "**.yml")
	}

	for _, path := range paths {
		match, err := ctx.Find(filepath.Join(dir, path))
		if err != nil {
			logger.Debugf("could not match glob pattern(%s): %v", dir+"/"+path, err)
			continue
		} else if len(match) == 0 {
			logger.Debugf("no files found in: %s", filepath.Join(dir, path))
		}

		matches = append(matches, match...) // using a seperate slice to avoid nested loops and complexity
	}

	return matches
}

func isYaml(filename string) bool {
	return filepath.Ext(filename) == ".yaml" || filepath.Ext(filename) == ".yml"
}
