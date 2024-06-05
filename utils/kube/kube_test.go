package kube

import "testing"

func TestGetGroupVersion(t *testing.T) {
	testCases := []struct {
		name            string
		apiVersion      string
		expectedGroup   string
		expectedVersion string
	}{
		{
			name:            "CoreAPIGroup",
			apiVersion:      "v1",
			expectedGroup:   "",
			expectedVersion: "v1",
		},
		{
			name:            "NamedAPIGroup",
			apiVersion:      "apps/v1",
			expectedGroup:   "apps",
			expectedVersion: "v1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			group, version := GetGroupVersion(tc.apiVersion)
			if group != tc.expectedGroup || version != tc.expectedVersion {
				t.Errorf("getGroupVersion(%q) = %q, %q; expected %q, %q",
					tc.apiVersion, group, version, tc.expectedGroup, tc.expectedVersion)
			}
		})
	}
}
