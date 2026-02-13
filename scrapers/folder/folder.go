package folder

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"time"

	"github.com/flanksource/artifacts"
	artifactFS "github.com/flanksource/artifacts/fs"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/gobwas/glob"
)

const (
	ConfigTypeFolderListing = "Folder::Listing"
	ConfigTypeFileMetadata  = "File::Metadata"
)

type FolderScraper struct{}

func (f FolderScraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.Folder) > 0
}

func (f FolderScraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults

	for _, config := range ctx.ScrapeConfig().Spec.Folder {
		result := f.scrapeFolder(ctx, config)
		results = append(results, result...)
	}

	return results
}

func (f FolderScraper) scrapeFolder(ctx api.ScrapeContext, config v1.Folder) v1.ScrapeResults {
	var results v1.ScrapeResults

	conn, err := config.GetConnection(ctx.DutyContext())
	if err != nil {
		return results.Errorf(err, "failed to get connection")
	}

	filesystem, err := artifacts.GetFSForConnection(ctx.DutyContext(), *conn)
	if err != nil {
		return results.Errorf(err, "failed to get filesystem")
	}

	path := config.GetPath()
	ctx.Logger.V(3).Infof("Scanning folder: %s (recursive=%v)", path, config.Recursive)

	filterCtx, err := newFolderFilterContext(config.Filter, config.Recursive)
	if err != nil {
		return results.Errorf(err, "failed to create filter context")
	}

	files, err := f.getFolderContents(ctx, filesystem, path, filterCtx)
	if err != nil {
		return results.Errorf(err, "failed to list folder contents")
	}

	ctx.Logger.V(3).Infof("Found %d files matching filter", len(files))

	for _, fileInfo := range files {
		result := f.createConfigItem(config, fileInfo, path)
		results = append(results, result)
	}

	return results
}

func (f FolderScraper) getFolderContents(ctx api.ScrapeContext, dirFS artifactFS.Filesystem, path string, filter *v1.FolderFilterContext) ([]fs.FileInfo, error) {
	files, err := dirFS.ReadDir(path)
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, nil
	}

	var result []fs.FileInfo
	for _, info := range files {
		if !f.filterFile(ctx, info, filter) {
			ctx.Logger.V(3).Infof("skipping %s, does not match filter", info.Name())
			continue
		}

		result = append(result, info)
	}

	return result, nil
}

func (f FolderScraper) filterFile(ctx api.ScrapeContext, info fs.FileInfo, filter *v1.FolderFilterContext) bool {
	// Skip directories unless explicitly allowed
	if info.IsDir() && !filter.AllowDir {
		return false
	}

	// Check age filters
	if filter.MinAge != nil {
		minTime := time.Now().Add(-time.Duration(*filter.MinAge))
		if info.ModTime().After(minTime) {
			return false
		}
	}

	if filter.MaxAge != nil {
		maxTime := time.Now().Add(-time.Duration(*filter.MaxAge))
		if info.ModTime().Before(maxTime) {
			return false
		}
	}

	// Check size filters
	if filter.Filter.MinSize != nil && info.Size() < *filter.Filter.MinSize {
		return false
	}

	if filter.Filter.MaxSize != nil && info.Size() > *filter.Filter.MaxSize {
		return false
	}

	// Check regex pattern
	if filter.RegexComp != nil {
		if re, ok := filter.RegexComp.(*regexp.Regexp); ok {
			if !re.MatchString(info.Name()) {
				return false
			}
		}
	}

	// Check glob pattern
	if filter.GlobComp != nil {
		if g, ok := filter.GlobComp.(glob.Glob); ok {
			if !g.Match(info.Name()) {
				return false
			}
		}
	}

	return true
}

func (f FolderScraper) createConfigItem(config v1.Folder, fileInfo fs.FileInfo, basePath string) v1.ScrapeResult {
	result := v1.NewScrapeResult(config.BaseScraper)

	// Create a unique ID for this file
	id := fmt.Sprintf("%s/%s", basePath, fileInfo.Name())

	// Determine the config type
	configType := ConfigTypeFileMetadata
	if fileInfo.IsDir() {
		configType = ConfigTypeFolderListing
	}

	// Override with user-defined type if provided
	if config.Type != "" {
		configType = config.Type
	}

	result.ID = id
	result.Type = configType
	result.ConfigClass = configType

	// Use filename as name if not specified
	if config.Name == "" {
		result.Name = fileInfo.Name()
	}

	// Create metadata JSON
	metadata := map[string]interface{}{
		"name":    fileInfo.Name(),
		"path":    basePath,
		"size":    fileInfo.Size(),
		"modTime": fileInfo.ModTime().Format(time.RFC3339),
		"isDir":   fileInfo.IsDir(),
		"mode":    fileInfo.Mode().String(),
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return result.Errorf("failed to marshal metadata: %v", err)
	}

	result.Config = metadata
	result.Source = fmt.Sprintf("%s/%s", basePath, fileInfo.Name())

	return result.Success(string(metadataJSON))
}

func newFolderFilterContext(filter v1.FolderFilter, allowDir bool) (*v1.FolderFilterContext, error) {
	filterCtx := &v1.FolderFilterContext{
		Filter:   filter,
		AllowDir: allowDir,
	}

	// Parse age durations
	if filter.MinAge != "" {
		duration, err := time.ParseDuration(filter.MinAge)
		if err != nil {
			return nil, fmt.Errorf("invalid minAge duration: %v", err)
		}
		filterCtx.MinAge = &duration
	}

	if filter.MaxAge != "" {
		duration, err := time.ParseDuration(filter.MaxAge)
		if err != nil {
			return nil, fmt.Errorf("invalid maxAge duration: %v", err)
		}
		filterCtx.MaxAge = &duration
	}

	// Compile regex if provided
	if filter.Regex != "" {
		re, err := regexp.Compile(filter.Regex)
		if err != nil {
			return nil, fmt.Errorf("invalid regex pattern: %v", err)
		}
		filterCtx.RegexComp = re
	}

	// Compile glob if provided
	if filter.Glob != "" {
		g, err := glob.Compile(filter.Glob)
		if err != nil {
			return nil, fmt.Errorf("invalid glob pattern: %v", err)
		}
		filterCtx.GlobComp = g
	}

	return filterCtx, nil
}
