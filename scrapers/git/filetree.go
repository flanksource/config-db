package git

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	v1 "github.com/flanksource/config-db/api/v1"
)

type FileEntry struct {
	Name            string    `json:"name"`
	SHA1            string    `json:"sha1"`
	Size            int64     `json:"size"`
	LastModified    time.Time `json:"lastModified"`
	LastModifiedSHA string    `json:"lastModifiedSHA"`
	LastModifiedBy  string    `json:"lastModifiedBy"`
}

type FileTree struct {
	Files []FileEntry `json:"files"`
}

func matchStrategy(path string, rules []v1.GitFileRule) v1.GitFileStrategy {
	matched := v1.GitFileStrategyTrack
	for _, rule := range rules {
		if strings.Contains(rule.Pattern, "**") {
			if ok, _ := doubleStarMatch(rule.Pattern, path); ok {
				matched = rule.Strategy
			}
		} else if ok, err := filepath.Match(rule.Pattern, path); err == nil && ok {
			matched = rule.Strategy
		}
	}
	return matched
}

// doubleStarMatch handles ** glob patterns that filepath.Match doesn't support.
// ** matches zero or more path segments (directories).
func doubleStarMatch(pattern, path string) (bool, error) {
	patParts := strings.Split(pattern, "/")
	pathParts := strings.Split(path, "/")
	return matchParts(patParts, pathParts), nil
}

func matchParts(patParts, pathParts []string) bool {
	for len(patParts) > 0 && len(pathParts) > 0 {
		if patParts[0] == "**" {
			patParts = patParts[1:]
			if len(patParts) == 0 {
				return true
			}
			for i := 0; i <= len(pathParts); i++ {
				if matchParts(patParts, pathParts[i:]) {
					return true
				}
			}
			return false
		}
		if ok, _ := filepath.Match(patParts[0], pathParts[0]); !ok {
			return false
		}
		patParts = patParts[1:]
		pathParts = pathParts[1:]
	}
	if len(patParts) == 0 && len(pathParts) == 0 {
		return true
	}
	if len(patParts) == 1 && patParts[0] == "**" {
		return true
	}
	return false
}

func buildFileTree(repo *git.Repository, commitHash plumbing.Hash, rules []v1.GitFileRule) (*FileTree, error) {
	commit, err := repo.CommitObject(commitHash)
	if err != nil {
		return nil, err
	}

	tree, err := commit.Tree()
	if err != nil {
		return nil, err
	}

	var entries []FileEntry
	err = tree.Files().ForEach(func(f *object.File) error {
		strategy := matchStrategy(f.Name, rules)
		if strategy == v1.GitFileStrategyIgnore {
			return nil
		}

		entry := FileEntry{
			Name: f.Name,
			SHA1: f.Hash.String(),
			Size: f.Size,
		}

		lastCommit, err := lastModifyingCommit(repo, commitHash, f.Name)
		if err == nil && lastCommit != nil {
			entry.LastModified = lastCommit.Author.When
			entry.LastModifiedSHA = lastCommit.Hash.String()
			entry.LastModifiedBy = fmt.Sprintf("%s <%s>", lastCommit.Author.Name, lastCommit.Author.Email)
		}

		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &FileTree{Files: entries}, nil
}

func lastModifyingCommit(repo *git.Repository, from plumbing.Hash, path string) (*object.Commit, error) {
	iter, err := repo.Log(&git.LogOptions{
		From:     from,
		FileName: &path,
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	return iter.Next()
}
