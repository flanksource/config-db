package git

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/samber/lo"

	v1 "github.com/flanksource/config-db/api/v1"
)

func generateCommitChanges(repo *git.Repository, branchRef *plumbing.Reference, branchExternalID string, rules []v1.GitFileRule, sinceCommit string, depth int) []v1.ChangeResult {
	var changes []v1.ChangeResult

	iter, err := repo.Log(&git.LogOptions{From: branchRef.Hash()})
	if err != nil {
		return changes
	}
	defer iter.Close()

	count := 0
	for depth <= 0 || count < depth {
		commit, err := iter.Next()
		if err != nil {
			break
		}

		if sinceCommit != "" && commit.Hash.String() == sinceCommit {
			break
		}

		change := commitToChange(repo, commit, branchExternalID, rules)
		if change != nil {
			changes = append(changes, *change)
		}
		count++
	}

	return changes
}

func commitToChange(repo *git.Repository, commit *object.Commit, branchExternalID string, rules []v1.GitFileRule) *v1.ChangeResult {
	var parentTree *object.Tree
	if commit.NumParents() > 0 {
		parent, err := commit.Parents().Next()
		if err == nil {
			parentTree, _ = parent.Tree()
		}
	}

	currentTree, err := commit.Tree()
	if err != nil {
		return nil
	}

	diffs, err := diffTrees(parentTree, currentTree)
	if err != nil {
		return nil
	}

	var trackedFiles, diffedFiles []string
	var diffContent strings.Builder

	for _, d := range diffs {
		strategy := matchStrategy(d.path, rules)
		switch strategy {
		case v1.GitFileStrategyIgnore:
			continue
		case v1.GitFileStrategyTrack:
			trackedFiles = append(trackedFiles, d.path)
		case v1.GitFileStrategyDiff:
			diffedFiles = append(diffedFiles, d.path)
			if d.patch != "" {
				diffContent.WriteString(fmt.Sprintf("--- %s ---\n%s\n", d.path, d.patch))
			}
		}
	}

	if len(trackedFiles) == 0 && len(diffedFiles) == 0 {
		return nil
	}

	createdAt := commit.Committer.When
	createdBy := fmt.Sprintf("%s <%s>", commit.Committer.Name, commit.Committer.Email)

	details := map[string]any{
		"message":   strings.TrimSpace(commit.Message),
		"author":    fmt.Sprintf("%s <%s>", commit.Author.Name, commit.Author.Email),
		"committer": fmt.Sprintf("%s <%s>", commit.Committer.Name, commit.Committer.Email),
	}
	if len(trackedFiles) > 0 {
		details["tracked_files"] = trackedFiles
	}
	if len(diffedFiles) > 0 {
		details["diffed_files"] = diffedFiles
	}

	result := &v1.ChangeResult{
		ExternalID:       branchExternalID,
		ConfigType:       "Git::Branch",
		ExternalChangeID: commit.Hash.String(),
		ChangeType:       "Commit",
		Summary:          firstLine(commit.Message),
		Source:           "git",
		CreatedAt:        &createdAt,
		CreatedBy:        &createdBy,
		Details:          details,
	}

	if diffContent.Len() > 0 {
		d := diffContent.String()
		result.Diff = &d
	}

	return result
}

type fileDiff struct {
	path  string
	patch string
}

func diffTrees(from, to *object.Tree) ([]fileDiff, error) {
	if from == nil {
		var diffs []fileDiff
		if to == nil {
			return diffs, nil
		}
		return treeAsAdded(to)
	}

	goChanges, err := from.Diff(to)
	if err != nil {
		return nil, err
	}

	var diffs []fileDiff
	for _, c := range goChanges {
		path := c.To.Name
		if path == "" {
			path = c.From.Name
		}

		var patch string
		p, err := c.Patch()
		if err == nil && p != nil {
			patch = p.String()
		}

		diffs = append(diffs, fileDiff{path: path, patch: patch})
	}
	return diffs, nil
}

func treeAsAdded(tree *object.Tree) ([]fileDiff, error) {
	var diffs []fileDiff
	err := tree.Files().ForEach(func(f *object.File) error {
		diffs = append(diffs, fileDiff{path: f.Name})
		return nil
	})
	return diffs, err
}

func resolveTagCommit(repo *git.Repository, ref *plumbing.Reference) (*object.Commit, error) {
	if tag, err := repo.TagObject(ref.Hash()); err == nil {
		return tag.Commit()
	}
	return repo.CommitObject(ref.Hash())
}

func generateTagChanges(repo *git.Repository, repoExternalID string, previousTags map[string]string, since time.Time) ([]v1.ChangeResult, map[string]string) {
	var changes []v1.ChangeResult
	currentTags := make(map[string]string)

	tags, err := repo.Tags()
	if err != nil {
		return changes, currentTags
	}

	_ = tags.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name().Short()

		commit, err := resolveTagCommit(repo, ref)
		if err != nil {
			return nil
		}
		sha := commit.Hash.String()

		if !since.IsZero() && commit.Committer.When.Before(since) {
			return nil
		}

		currentTags[name] = sha

		if _, existed := previousTags[name]; !existed {
			now := time.Now()
			changes = append(changes, v1.ChangeResult{
				ExternalID:       repoExternalID,
				ConfigType:       "Git::Repository",
				ExternalChangeID: fmt.Sprintf("tag/%s/%s", name, sha),
				ChangeType:       "Tag",
				Summary:          fmt.Sprintf("Tag %s created", name),
				Source:           "git",
				CreatedAt:        &now,
				Details:          map[string]any{"tag": name, "sha": sha},
			})
		}
		return nil
	})

	for name, sha := range previousTags {
		if _, exists := currentTags[name]; !exists {
			now := time.Now()
			changes = append(changes, v1.ChangeResult{
				ExternalID:       repoExternalID,
				ConfigType:       "Git::Repository",
				ExternalChangeID: fmt.Sprintf("tag/%s/%s/delete", name, sha),
				ChangeType:       "Tag",
				Summary:          fmt.Sprintf("Tag %s deleted", name),
				Source:           "git",
				CreatedAt:        &now,
				Details:          map[string]any{"tag": name},
			})
		}
	}

	return changes, currentTags
}

func generateTagChangesForBranch(repo *git.Repository, branchExternalID string, branchRef *plumbing.Reference, previousTags map[string]string, since time.Time) []v1.ChangeResult {
	var changes []v1.ChangeResult

	tags, err := repo.Tags()
	if err != nil {
		return changes
	}

	_ = tags.ForEach(func(ref *plumbing.Reference) error {
		name := ref.Name().Short()

		commit, err := resolveTagCommit(repo, ref)
		if err != nil {
			return nil
		}
		sha := commit.Hash.String()

		if !since.IsZero() && commit.Committer.When.Before(since) {
			return nil
		}

		if _, existed := previousTags[name]; existed {
			return nil
		}

		if isAncestor(repo, plumbing.NewHash(sha), branchRef.Hash()) {
			now := time.Now()
			changes = append(changes, v1.ChangeResult{
				ExternalID:       branchExternalID,
				ConfigType:       "Git::Branch",
				ExternalChangeID: fmt.Sprintf("tag/%s/%s", name, sha),
				ChangeType:       "Tag",
				Summary:          fmt.Sprintf("Tag %s created", name),
				Source:           "git",
				CreatedAt:        &now,
				Details:          map[string]any{"tag": name, "sha": sha},
			})
		}
		return nil
	})

	return changes
}

func isAncestor(repo *git.Repository, candidate, head plumbing.Hash) bool {
	if candidate == head {
		return true
	}

	iter, err := repo.Log(&git.LogOptions{From: head})
	if err != nil {
		return false
	}
	defer iter.Close()

	found := false
	_ = iter.ForEach(func(c *object.Commit) error {
		if c.Hash == candidate {
			found = true
			return storer.ErrStop
		}
		return nil
	})
	return found
}

func generateBranchChanges(repoExternalID string, currentBranches []string, previousBranches []string) []v1.ChangeResult {
	var changes []v1.ChangeResult
	now := time.Now()

	prevSet := lo.SliceToMap(previousBranches, func(b string) (string, bool) { return b, true })
	currSet := lo.SliceToMap(currentBranches, func(b string) (string, bool) { return b, true })

	for _, b := range currentBranches {
		if !prevSet[b] {
			changes = append(changes, v1.ChangeResult{
				ExternalID:       repoExternalID,
				ConfigType:       "Git::Repository",
				ExternalChangeID: fmt.Sprintf("branch/%s/create", b),
				ChangeType:       "Branch",
				Summary:          fmt.Sprintf("Branch %s created", b),
				Source:           "git",
				CreatedAt:        &now,
				Details:          map[string]any{"branch": b},
			})
		}
	}

	for _, b := range previousBranches {
		if !currSet[b] {
			changes = append(changes, v1.ChangeResult{
				ExternalID:       repoExternalID,
				ConfigType:       "Git::Repository",
				ExternalChangeID: fmt.Sprintf("branch/%s/delete", b),
				ChangeType:       "Branch",
				Summary:          fmt.Sprintf("Branch %s deleted", b),
				Source:           "git",
				CreatedAt:        &now,
				Details:          map[string]any{"branch": b},
			})
		}
	}

	return changes
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}
