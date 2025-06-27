package gcp

import (
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

func isConsonant(b byte) bool {
	vowels := "aeiouAEIOU"
	return !strings.ContainsRune(vowels, rune(b))
}

// pluralToKind tries to convert kubernetes plural resource name to it's kind
func pluralToKind(plural string) string {
	// Handle irregular cases first
	irregulars := map[string]string{
		"endpoints":       "Endpoints",
		"networkpolicies": "NetworkPolicy",
		// Add other known irregulars
	}

	if kind, exists := irregulars[plural]; exists {
		return kind
	}

	caser := cases.Title(language.English)

	// Handle regular patterns
	switch {
	case strings.HasSuffix(plural, "ies"):
		// policies → Policy, gateways → Gateway (need context to distinguish)
		base := plural[:len(plural)-3]
		if isConsonant(base[len(base)-1]) {
			return caser.String(base + "y")
		}
		return caser.String(base + "ys") // This case is ambiguous

	case strings.HasSuffix(plural, "ves"):
		// shelves → Shelf
		base := plural[:len(plural)-3]
		return caser.String(base + "f")

	case strings.HasSuffix(plural, "ses"):
		// classes → Class, meshes → Mesh
		base := plural[:len(plural)-2]
		return caser.String(base)

	case strings.HasSuffix(plural, "es"):
		// Check if it's likely an 'es' addition or just 's'
		base := plural[:len(plural)-2]
		if strings.HasSuffix(base, "s") || strings.HasSuffix(base, "sh") ||
			strings.HasSuffix(base, "ch") || strings.HasSuffix(base, "x") ||
			strings.HasSuffix(base, "z") {
			return caser.String(base)
		}
		// Otherwise it's probably just 's' → remove 's'
		return caser.String(plural[:len(plural)-1])

	default:
		// Remove trailing 's'
		if strings.HasSuffix(plural, "s") {
			return caser.String(plural[:len(plural)-1])
		}
		return caser.String(plural)
	}
}
