package git

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	goGit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
	"github.com/flanksource/duty/connection"
)

type Scraper struct{}

func (s Scraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.Git) > 0
}

func (s Scraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults
	for _, config := range ctx.ScrapeConfig().Spec.Git {
		r := scrapeRepository(ctx, config)
		results = append(results, r...)
	}
	return results
}

func scrapeRepository(ctx api.ScrapeContext, config v1.Git) v1.ScrapeResults {
	var results v1.ScrapeResults

	gitConn := config.GitConnection
	if err := gitConn.HydrateConnection(ctx.DutyContext()); err != nil {
		return results.Errorf(err, "failed to hydrate git connection")
	}

	gitClient, err := connection.CreateGitConfig(ctx.DutyContext(), &gitConn)
	if err != nil {
		return results.Errorf(err, "failed to create git config")
	}

	dir := filepath.Join(os.TempDir(), "config-db-git", utils.Sha256Hex(gitClient.URL))
	if _, err := gitClient.Clone(ctx.DutyContext(), dir); err != nil {
		return results.Errorf(err, "failed to clone repository")
	}

	repo, err := goGit.PlainOpen(dir)
	if err != nil {
		return results.Errorf(err, "failed to open repository")
	}

	repoExternalID := repoID(gitClient.GetShortURL())

	// Load previous state from TempCache
	previousTags := loadPreviousTags(ctx, repoExternalID)
	previousBranches := loadPreviousBranches(ctx, repoExternalID)

	// Build repo-level config
	headRef, err := repo.Head()
	if err != nil {
		return results.Errorf(err, "failed to get HEAD")
	}

	fileTree, err := buildFileTree(repo, headRef.Hash(), config.Files)
	if err != nil {
		return results.Errorf(err, "failed to build file tree")
	}

	repoConfig := map[string]any{
		"url":     gitClient.URL,
		"branch":  gitClient.Branch,
		"headSHA": headRef.Hash().String(),
		"files":   fileTree.Files,
	}

	repoResult := v1.NewScrapeResult(config.BaseScraper)
	repoResult.ID = repoExternalID
	repoResult.Type = "Git::Repository"
	repoResult.ConfigClass = "Repository"
	repoResult.Name = gitClient.GetShortURL()
	repoResult.Tags = v1.JSONStringMap{
		"url":           gitClient.URL,
		"defaultBranch": gitClient.Branch,
	}
	results = append(results, repoResult.Success(repoConfig))

	// Discover remote branches
	remoteBranches := listRemoteBranches(repo)

	// Match branches against configured patterns
	trackedBranches := filterBranches(remoteBranches, config.Branches, gitClient.Branch)

	// Generate branch lifecycle changes on repo
	branchChanges := generateBranchChanges(repoExternalID, branchNames(trackedBranches), previousBranches)
	for _, c := range branchChanges {
		results.AddChange(config.BaseScraper, c)
	}

	// Compute since from the oldest commit in the depth window
	depth := gitClient.Depth
	if depth == 0 {
		depth = 50
	}
	tagSince := oldestCommitTime(repo, headRef.Hash(), depth)

	// Generate tag changes on repo
	tagChanges, currentTags := generateTagChanges(repo, repoExternalID, previousTags, tagSince)
	for _, c := range tagChanges {
		results.AddChange(config.BaseScraper, c)
	}

	// Store current tags in repo config for next run
	repoConfig["tags"] = currentTags

	// Process each tracked branch
	for _, branchRef := range trackedBranches {
		branchName := branchRef.Name().Short()
		branchName = strings.TrimPrefix(branchName, "origin/")
		branchExtID := branchID(gitClient.GetShortURL(), branchName)

		branchTree, err := buildFileTree(repo, branchRef.Hash(), config.Files)
		if err != nil {
			ctx.Logger.Errorf("failed to build file tree for branch %s: %v", branchName, err)
			continue
		}

		branchConfig := map[string]any{
			"branch":  branchName,
			"headSHA": branchRef.Hash().String(),
			"files":   branchTree.Files,
		}

		branchResult := v1.NewScrapeResult(config.BaseScraper)
		branchResult.ID = branchExtID
		branchResult.Type = "Git::Branch"
		branchResult.ConfigClass = "Branch"
		branchResult.Name = branchName
		branchResult.Tags = v1.JSONStringMap{
			"branch": branchName,
			"url":    gitClient.URL,
		}
		branchResult.Parents = []v1.ConfigExternalKey{{
			ExternalID: repoExternalID,
			Type:       "Git::Repository",
		}}

		sinceCommit := loadPreviousHeadSHA(ctx, branchExtID)

		commitChanges := generateCommitChanges(repo, branchRef, branchExtID, config.Files, sinceCommit, depth)
		branchResult.Changes = commitChanges

		// Tag changes on branch
		branchSince := oldestCommitTime(repo, branchRef.Hash(), depth)
		branchTagChanges := generateTagChangesForBranch(repo, branchExtID, branchRef, previousTags, branchSince)
		branchResult.Changes = append(branchResult.Changes, branchTagChanges...)

		results = append(results, branchResult.Success(branchConfig))
	}

	return results
}

func repoID(shortURL string) string {
	return fmt.Sprintf("git/%s", shortURL)
}

func branchID(shortURL, branch string) string {
	return fmt.Sprintf("git/%s::%s", shortURL, branch)
}

func listRemoteBranches(repo *goGit.Repository) []*plumbing.Reference {
	var refs []*plumbing.Reference

	refIter, err := repo.References()
	if err != nil {
		return refs
	}

	_ = refIter.ForEach(func(ref *plumbing.Reference) error {
		if ref.Name().IsRemote() {
			name := ref.Name().Short()
			if !strings.HasSuffix(name, "/HEAD") {
				refs = append(refs, ref)
			}
		}
		return nil
	})

	return refs
}

func filterBranches(remoteBranches []*plumbing.Reference, patterns []string, defaultBranch string) []*plumbing.Reference {
	if len(patterns) == 0 {
		patterns = []string{defaultBranch}
	}

	var matched []*plumbing.Reference
	for _, ref := range remoteBranches {
		name := strings.TrimPrefix(ref.Name().Short(), "origin/")
		for _, pattern := range patterns {
			if ok, _ := filepath.Match(pattern, name); ok {
				matched = append(matched, ref)
				break
			}
		}
	}
	return matched
}

func branchNames(refs []*plumbing.Reference) []string {
	var names []string
	for _, ref := range refs {
		names = append(names, strings.TrimPrefix(ref.Name().Short(), "origin/"))
	}
	return names
}

func oldestCommitTime(repo *goGit.Repository, head plumbing.Hash, depth int) time.Time {
	iter, err := repo.Log(&goGit.LogOptions{From: head})
	if err != nil {
		return time.Time{}
	}
	defer iter.Close()

	var oldest time.Time
	for i := 0; depth <= 0 || i < depth; i++ {
		commit, err := iter.Next()
		if err != nil {
			break
		}
		oldest = commit.Committer.When
	}
	return oldest
}

func loadPreviousTags(ctx api.ScrapeContext, repoExternalID string) map[string]string {
	configMap := loadCachedConfig(ctx, repoExternalID, "Git::Repository")
	if configMap == nil {
		return make(map[string]string)
	}

	tagsRaw, ok := configMap["tags"]
	if !ok {
		return make(map[string]string)
	}

	tags, ok := tagsRaw.(map[string]any)
	if !ok {
		return make(map[string]string)
	}

	result := make(map[string]string, len(tags))
	for k, v := range tags {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	return result
}

func loadPreviousBranches(_ api.ScrapeContext, _ string) []string {
	// FIXME: implement branch tracking from TempCache
	return nil
}

func loadPreviousHeadSHA(ctx api.ScrapeContext, branchExternalID string) string {
	configMap := loadCachedConfig(ctx, branchExternalID, "Git::Branch")
	if configMap == nil {
		return ""
	}
	if sha, ok := configMap["headSHA"].(string); ok {
		return sha
	}
	return ""
}

func loadCachedConfig(ctx api.ScrapeContext, externalID, configType string) map[string]any {
	cache := ctx.TempCache()
	if cache == nil {
		return nil
	}

	item, err := cache.Find(ctx, v1.ExternalID{
		ExternalID: externalID,
		ConfigType: configType,
	})
	if err != nil || item == nil || item.Config == nil {
		return nil
	}

	var configMap map[string]any
	if err := json.Unmarshal([]byte(*item.Config), &configMap); err != nil {
		return nil
	}
	return configMap
}
