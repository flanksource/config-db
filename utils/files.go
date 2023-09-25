package utils

import (
	"os"
	"path/filepath"
)

func Find(path string) ([]string, error) {
	return filepath.Glob(path)
}

// Read returns the contents of a file, the base filename and an error
func Read(path string) ([]byte, string, error) {
	content, err := os.ReadFile(path)
	filename := filepath.Base(path)
	return content, filename, err
}
