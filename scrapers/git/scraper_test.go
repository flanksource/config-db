package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	goGit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	v1 "github.com/flanksource/config-db/api/v1"
)

func TestMatchStrategy(t *testing.T) {
	// Rules are evaluated in order; last matching rule wins.
	// Order: catch-all first, then specific overrides.
	rules := []v1.GitFileRule{
		{Pattern: "**/*", Strategy: v1.GitFileStrategyTrack},
		{Pattern: "**/*.yaml", Strategy: v1.GitFileStrategyDiff},
		{Pattern: "**/*.tf", Strategy: v1.GitFileStrategyDiff},
		{Pattern: "vendor/**", Strategy: v1.GitFileStrategyIgnore},
	}

	tests := []struct {
		path     string
		expected v1.GitFileStrategy
	}{
		{"vendor/lib/foo.go", v1.GitFileStrategyIgnore},
		{"kubernetes/app/values.yaml", v1.GitFileStrategyDiff},
		{"main.tf", v1.GitFileStrategyDiff},
		{"README.md", v1.GitFileStrategyTrack},
		{"src/config.json", v1.GitFileStrategyTrack},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := matchStrategy(tt.path, rules)
			if got != tt.expected {
				t.Errorf("matchStrategy(%q) = %q, want %q", tt.path, got, tt.expected)
			}
		})
	}
}

func TestMatchStrategyDefault(t *testing.T) {
	got := matchStrategy("any/file.go", nil)
	if got != v1.GitFileStrategyTrack {
		t.Errorf("matchStrategy with no rules = %q, want %q", got, v1.GitFileStrategyTrack)
	}
}

func TestMatchStrategyLastRuleWins(t *testing.T) {
	rules := []v1.GitFileRule{
		{Pattern: "**/*", Strategy: v1.GitFileStrategyIgnore},
		{Pattern: "**/*", Strategy: v1.GitFileStrategyDiff},
	}

	got := matchStrategy("file.txt", rules)
	if got != v1.GitFileStrategyDiff {
		t.Errorf("last matching rule should win: got %q, want %q", got, v1.GitFileStrategyDiff)
	}
}

func initTestRepo(t *testing.T) (string, *goGit.Repository) {
	t.Helper()
	dir := t.TempDir()

	repo, err := goGit.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "README.md", "# Test Repo")
	writeFile(t, dir, "main.tf", "resource \"aws_instance\" \"web\" {}")
	writeFile(t, dir, "config.yaml", "key: value")
	writeFile(t, dir, "vendor/lib/foo.go", "package lib")

	if _, err := wt.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("main.tf"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("config.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("vendor/lib/foo.go"); err != nil {
		t.Fatal(err)
	}

	_, err = wt.Commit("initial commit", &goGit.CommitOptions{
		Author: &object.Signature{
			Name:  "Test",
			Email: "test@example.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	return dir, repo
}

func writeFile(t *testing.T, dir, path, content string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildFileTree(t *testing.T) {
	_, repo := initTestRepo(t)

	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}

	rules := []v1.GitFileRule{
		{Pattern: "vendor/**", Strategy: v1.GitFileStrategyIgnore},
	}

	tree, err := buildFileTree(repo, head.Hash(), rules)
	if err != nil {
		t.Fatal(err)
	}

	fileNames := make(map[string]bool)
	for _, f := range tree.Files {
		fileNames[f.Name] = true
	}

	if fileNames["vendor/lib/foo.go"] {
		t.Error("vendor/lib/foo.go should be excluded by ignore rule")
	}
	if !fileNames["README.md"] {
		t.Error("README.md should be included")
	}
	if !fileNames["main.tf"] {
		t.Error("main.tf should be included")
	}
	if !fileNames["config.yaml"] {
		t.Error("config.yaml should be included")
	}

	for _, f := range tree.Files {
		if f.SHA1 == "" {
			t.Errorf("file %s has empty SHA1", f.Name)
		}
		if f.LastModifiedBy != "Test <test@example.com>" {
			t.Errorf("file %s lastModifiedBy = %q, want %q", f.Name, f.LastModifiedBy, "Test <test@example.com>")
		}
	}
}

func TestGenerateCommitChanges(t *testing.T) {
	dir, repo := initTestRepo(t)

	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "config.yaml", "key: updated")
	if _, err := wt.Add("config.yaml"); err != nil {
		t.Fatal(err)
	}
	_, err = wt.Commit("update config", &goGit.CommitOptions{
		Author: &object.Signature{
			Name:  "Test",
			Email: "test@example.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}

	rules := []v1.GitFileRule{
		{Pattern: "vendor/**", Strategy: v1.GitFileStrategyIgnore},
		{Pattern: "*.yaml", Strategy: v1.GitFileStrategyDiff},
	}

	changes := generateCommitChanges(repo, head, "git/test::main", rules, "", 10)

	if len(changes) != 2 {
		t.Fatalf("expected 2 commit changes (initial + update), got %d", len(changes))
	}

	// Most recent commit first
	if changes[0].Summary != "update config" {
		t.Errorf("first change summary = %q, want %q", changes[0].Summary, "update config")
	}
	if changes[0].ChangeType != "Commit" {
		t.Errorf("change type = %q, want Commit", changes[0].ChangeType)
	}
	if changes[0].Diff == nil {
		t.Error("diff strategy file should have diff content")
	}
	if *changes[0].CreatedBy != "Test <test@example.com>" {
		t.Errorf("created_by = %q, want %q", *changes[0].CreatedBy, "Test <test@example.com>")
	}
	if changes[0].Details["author"] != "Test <test@example.com>" {
		t.Errorf("author = %q, want %q", changes[0].Details["author"], "Test <test@example.com>")
	}
	if _, ok := changes[0].Details["committer"]; !ok {
		t.Error("commit details should contain committer")
	}
}

func TestGenerateCommitChangesSinceCommit(t *testing.T) {
	dir, repo := initTestRepo(t)

	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	sinceCommit := head.Hash().String()

	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "new.txt", "hello")
	if _, err := wt.Add("new.txt"); err != nil {
		t.Fatal(err)
	}
	_, err = wt.Commit("add new file", &goGit.CommitOptions{
		Author: &object.Signature{
			Name:  "Test",
			Email: "test@example.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	newHead, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}

	changes := generateCommitChanges(repo, newHead, "git/test::main", nil, sinceCommit, 100)

	if len(changes) != 1 {
		t.Fatalf("expected 1 change since last commit, got %d", len(changes))
	}
	if changes[0].Summary != "add new file" {
		t.Errorf("change summary = %q, want %q", changes[0].Summary, "add new file")
	}
}

func TestGenerateBranchChanges(t *testing.T) {
	current := []string{"main", "develop", "feature/x"}
	previous := []string{"main", "release/1.0"}

	changes := generateBranchChanges("git/test", current, previous)

	created := 0
	deleted := 0
	for _, c := range changes {
		if c.ChangeType != "Branch" {
			t.Errorf("unexpected change type %q", c.ChangeType)
			continue
		}
		if strings.Contains(c.Summary, "created") {
			created++
		} else if strings.Contains(c.Summary, "deleted") {
			deleted++
		}
	}

	if created != 2 {
		t.Errorf("expected 2 branch creates (develop, feature/x), got %d", created)
	}
	if deleted != 1 {
		t.Errorf("expected 1 branch delete (release/1.0), got %d", deleted)
	}
}

func TestGenerateTagChanges(t *testing.T) {
	dir, repo := initTestRepo(t)

	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}

	// Create a tag
	_, err = repo.CreateTag("v1.0.0", head.Hash(), &goGit.CreateTagOptions{
		Tagger: &object.Signature{
			Name:  "Test",
			Email: "test@example.com",
			When:  time.Now(),
		},
		Message: "release v1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}

	_ = dir

	changes, currentTags := generateTagChanges(repo, "git/test", map[string]string{}, time.Time{})

	if len(changes) != 1 {
		t.Fatalf("expected 1 tag create change, got %d", len(changes))
	}
	if changes[0].ChangeType != "Tag" {
		t.Errorf("change type = %q, want GitTagCreate", changes[0].ChangeType)
	}
	if currentTags["v1.0.0"] == "" {
		t.Error("v1.0.0 should be in current tags")
	}

	// Now test tag delete detection
	changes2, _ := generateTagChanges(repo, "git/test", map[string]string{
		"v1.0.0": currentTags["v1.0.0"],
		"v0.9.0": "abc123",
	}, time.Time{})

	deleteFound := false
	for _, c := range changes2 {
		if c.ChangeType == "Tag" && strings.Contains(c.Summary, "deleted") {
			deleteFound = true
			if fmt.Sprintf("%v", c.Details["tag"]) != "v0.9.0" {
				t.Errorf("deleted tag = %v, want v0.9.0", c.Details["tag"])
			}
		}
	}
	if !deleteFound {
		t.Error("expected a tag delete change for v0.9.0")
	}
}

func TestGenerateTagChangesFilteredBySince(t *testing.T) {
	dir, repo := initTestRepo(t)

	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}

	oldTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	// Create a tag on the initial commit (which has a recent committer date)
	_, err = repo.CreateTag("v1.0.0", head.Hash(), &goGit.CreateTagOptions{
		Tagger:  &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()},
		Message: "release v1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create a second commit with an old date, then tag it
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "old.txt", "old content")
	if _, err := wt.Add("old.txt"); err != nil {
		t.Fatal(err)
	}
	oldHash, err := wt.Commit("old commit", &goGit.CommitOptions{
		Author:    &object.Signature{Name: "Test", Email: "test@example.com", When: oldTime},
		Committer: &object.Signature{Name: "Test", Email: "test@example.com", When: oldTime},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = repo.CreateTag("v0.1.0", oldHash, &goGit.CreateTagOptions{
		Tagger:  &object.Signature{Name: "Test", Email: "test@example.com", When: oldTime},
		Message: "old release",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Use a since that excludes the old commit but includes the recent one
	since := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	changes, currentTags := generateTagChanges(repo, "git/test", map[string]string{}, since)

	if _, ok := currentTags["v0.1.0"]; ok {
		t.Error("v0.1.0 should be excluded by since filter")
	}
	if _, ok := currentTags["v1.0.0"]; !ok {
		t.Error("v1.0.0 should be included (commit is recent)")
	}
	if len(changes) != 1 {
		t.Fatalf("expected 1 tag change (v1.0.0 created), got %d", len(changes))
	}
	if changes[0].Details["tag"] != "v1.0.0" {
		t.Errorf("expected tag v1.0.0, got %v", changes[0].Details["tag"])
	}
}

func TestFilterBranches(t *testing.T) {
	refs := []*plumbing.Reference{
		plumbing.NewReferenceFromStrings("refs/remotes/origin/main", "abc123"),
		plumbing.NewReferenceFromStrings("refs/remotes/origin/develop", "def456"),
		plumbing.NewReferenceFromStrings("refs/remotes/origin/release/1.0", "ghi789"),
		plumbing.NewReferenceFromStrings("refs/remotes/origin/release/2.0", "jkl012"),
	}

	matched := filterBranches(refs, []string{"main", "release/*"}, "main")

	names := branchNames(matched)
	if len(names) != 3 {
		t.Fatalf("expected 3 matched branches, got %d: %v", len(names), names)
	}
}

func TestFirstLine(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"single line", "single line"},
		{"first\nsecond\nthird", "first"},
		{"  padded  \n  second  ", "padded  "},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := firstLine(tt.input); got != tt.expected {
				t.Errorf("firstLine(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestRepoAndBranchID(t *testing.T) {
	repoID := repoID("github.com/myorg/infra")
	if repoID != "git/github.com/myorg/infra" {
		t.Errorf("repoID = %q, want git/github.com/myorg/infra", repoID)
	}

	branchID := branchID("github.com/myorg/infra", "main")
	if branchID != "git/github.com/myorg/infra::main" {
		t.Errorf("branchID = %q, want git/github.com/myorg/infra::main", branchID)
	}
}
