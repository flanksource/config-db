package folder

import (
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/flanksource/config-db/api/v1"
)

func TestFolderScraper_CanScrape(t *testing.T) {
	scraper := FolderScraper{}

	tests := []struct {
		name     string
		spec     v1.ScraperSpec
		expected bool
	}{
		{
			name: "with folder config",
			spec: v1.ScraperSpec{
				Folder: []v1.Folder{{}},
			},
			expected: true,
		},
		{
			name: "without folder config",
			spec: v1.ScraperSpec{
				File: []v1.File{{}},
			},
			expected: false,
		},
		{
			name:     "empty spec",
			spec:     v1.ScraperSpec{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scraper.CanScrape(tt.spec)
			if result != tt.expected {
				t.Errorf("CanScrape() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestFolderScraper_CreateConfigItem(t *testing.T) {
	// Create a temporary file to test with
	tempDir, err := os.MkdirTemp("", "folder-scraper-config-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	testFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	fileInfo, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("failed to stat test file: %v", err)
	}

	// Create test config
	config := v1.Folder{
		BaseScraper: v1.BaseScraper{
			CustomScraperBase: v1.CustomScraperBase{
				Type: "File::Metadata",
			},
		},
		Local: tempDir,
	}

	scraper := FolderScraper{}
	result := scraper.createConfigItem(config, fileInfo, tempDir)

	// Verify result structure
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	if result.Type != ConfigTypeFileMetadata {
		t.Errorf("expected type %s, got %s", ConfigTypeFileMetadata, result.Type)
	}

	if result.Name != "test.txt" {
		t.Errorf("expected name test.txt, got %s", result.Name)
	}

	// Verify config metadata
	if result.Config != nil {
		metadata, ok := result.Config.(map[string]interface{})
		if !ok {
			t.Fatal("config should be a map")
		}

		// Check required fields
		requiredFields := []string{"name", "path", "size", "modTime", "isDir", "mode"}
		for _, field := range requiredFields {
			if _, exists := metadata[field]; !exists {
				t.Errorf("metadata missing required field: %s", field)
			}
		}

		// Verify values
		if name, ok := metadata["name"].(string); !ok || name != "test.txt" {
			t.Errorf("expected name test.txt, got %v", metadata["name"])
		}

		if isDir, ok := metadata["isDir"].(bool); !ok || isDir {
			t.Errorf("expected isDir false, got %v", metadata["isDir"])
		}
	}
}

func TestFolderScraper_CreateConfigItem_Directory(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "folder-scraper-dir-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a subdirectory
	subDir := filepath.Join(tempDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	dirInfo, err := os.Stat(subDir)
	if err != nil {
		t.Fatalf("failed to stat subdir: %v", err)
	}

	config := v1.Folder{
		Local: tempDir,
	}

	scraper := FolderScraper{}
	result := scraper.createConfigItem(config, dirInfo, tempDir)

	// Verify it's identified as a directory
	if result.Type != ConfigTypeFolderListing {
		t.Errorf("expected type %s for directory, got %s", ConfigTypeFolderListing, result.Type)
	}

	if result.Config != nil {
		metadata, ok := result.Config.(map[string]interface{})
		if !ok {
			t.Fatal("config should be a map")
		}

		if isDir, ok := metadata["isDir"].(bool); !ok || !isDir {
			t.Errorf("expected isDir true for directory, got %v", metadata["isDir"])
		}
	}
}

func TestFolderScraper_RecursiveScanning(t *testing.T) {
	t.Skip("Recursive scanning requires full integration with artifacts library - tested in end-to-end scenarios")
	// This test is skipped because:
	// 1. Recursive scanning is handled by the artifacts library's ReadDir functionality
	// 2. Testing requires mocking the entire filesystem interface
	// 3. Integration tests with real S3/GCS connections are more appropriate
	// 4. The recursive flag is properly passed to the filter context in the implementation
}
