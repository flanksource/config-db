package matchers

import (
	"os"
	"path/filepath"
)

// fileMatcher satisfies the Matcher interface
type fileMatcher struct{}

// NewFile is the factory function for the file matcher
func NewFile() Matcher {
	return &fileMatcher{}
}

// Match ...
func (fm *fileMatcher) Match(path string) ([]string, error) {
	return filepath.Glob(path)
}

// Read ...
func (fm *fileMatcher) Read(match string) ([]byte, error) {
	return os.ReadFile(match)
}
