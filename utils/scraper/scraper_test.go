package scraper

import (
	"testing"
)

func TestGetConnectionNameType(t *testing.T) {
	testCases := []struct {
		name       string
		connection string
		expect     struct {
			name           string
			connectionType string
			found          bool
		}
	}{
		{
			name:       "valid connection string",
			connection: "connection://db/mission_control",
			expect: struct {
				name           string
				connectionType string
				found          bool
			}{
				name:           "mission_control",
				connectionType: "db",
				found:          true,
			},
		},
		{
			name:       "valid connection string | name has /",
			connection: "connection://db/mission_control//",
			expect: struct {
				name           string
				connectionType string
				found          bool
			}{
				name:           "mission_control//",
				connectionType: "db",
				found:          true,
			},
		},
		{
			name:       "invalid | host only",
			connection: "connection:///type-only",
			expect: struct {
				name           string
				connectionType string
				found          bool
			}{
				name:           "",
				connectionType: "",
				found:          false,
			},
		},
		{
			name:       "invalid connection string",
			connection: "invalid-connection-string",
			expect: struct {
				name           string
				connectionType string
				found          bool
			}{
				name:           "",
				connectionType: "",
				found:          false,
			},
		},
		{
			name:       "empty connection string",
			connection: "",
			expect: struct {
				name           string
				connectionType string
				found          bool
			}{
				name:           "",
				connectionType: "",
				found:          false,
			},
		},
		{
			name:       "connection string with type only",
			connection: "connection://type-only",
			expect: struct {
				name           string
				connectionType string
				found          bool
			}{
				name:           "",
				connectionType: "",
				found:          false,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			name, connectionType, found := extractConnectionNameType(tc.connection)
			if name != tc.expect.name {
				t.Errorf("expected name %q, but got %q", tc.expect.name, name)
			}
			if connectionType != tc.expect.connectionType {
				t.Errorf("expected connection type %q, but got %q", tc.expect.connectionType, connectionType)
			}
			if found != tc.expect.found {
				t.Errorf("expected found %t, but got %t", tc.expect.found, found)
			}
		})
	}
}
