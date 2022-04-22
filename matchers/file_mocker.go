package matchers

import (
	"fmt"

	"github.com/gobwas/glob"
)

// mockMatcher satisfies the Matcher interface for testing purposes
type mockMatcher struct {
	configFiles map[string]string
}

// NewMock is the factory function for the Mocker Matcher
func NewMock(cfs map[string]string) Matcher {
	return &mockMatcher{
		configFiles: cfs,
	}
}

func (mm *mockMatcher) Match(path string) ([]string, error) {
	g := glob.MustCompile(path)

	matches := []string{}

	for cf := range mm.configFiles {
		if g.Match(cf) {
			matches = append(matches, cf)
		}
	}

	return matches, nil
}

func (mm *mockMatcher) Read(filename string) ([]byte, error) {
	content, exists := mm.configFiles[filename]
	if !exists {
		return nil, fmt.Errorf("no such file:%s", filename)
	}
	return []byte(content), nil
}
