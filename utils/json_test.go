package utils

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

type TestStruct struct {
	Name    string `json:"name,omitempty"`
	Age     int    `json:"age,omitempty"`
	Email   string `json:"email,omitempty"`
	Country string `json:"country,omitempty"`
}

func TestStructToMap(t *testing.T) {
	testCases := []struct {
		name        string
		input       interface{}
		expectedMap map[string]any
		expectError bool
	}{
		{
			name: "Struct with string fields",
			input: TestStruct{
				Name:    "John Doe",
				Age:     30,
				Email:   "john.doe@example.com",
				Country: "USA",
			},
			expectedMap: map[string]any{
				"name":    "John Doe",
				"age":     float64(30),
				"email":   "john.doe@example.com",
				"country": "USA",
			},
			expectError: false,
		},
		{
			name:        "Empty struct",
			input:       TestStruct{},
			expectedMap: map[string]any{},
			expectError: false,
		},
		{
			name:        "Non-serializable input",
			input:       make(chan int),
			expectedMap: nil,
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resultMap, err := StructToMap(tc.input)
			if tc.expectError {
				if err == nil {
					t.Errorf("Expected an error, but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, but got: %v", err)
				}
			}

			if diff := cmp.Diff(resultMap, tc.expectedMap); diff != "" {
				t.Errorf("Result map differs from expected: %v", diff)
			}
		})
	}
}
