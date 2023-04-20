package scraper

import "strings"

func ExtractConnectionNameType(connectionString string) (name string, connectionType string, found bool) {
	prefix := "connection://"

	if !strings.HasPrefix(connectionString, prefix) {
		return
	}

	connectionString = strings.TrimPrefix(connectionString, prefix)
	parts := strings.SplitN(connectionString, "/", 2)
	if len(parts) != 2 {
		return
	}

	if parts[0] == "" || parts[1] == "" {
		return
	}

	return parts[1], parts[0], true
}
