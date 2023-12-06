package utils

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
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
			resultMap, err := ToJSONMap(tc.input)
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

func Test_ExtractChangedPaths(t *testing.T) {
	type args struct {
		data  string
		paths []string
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "single child on all levels",
			args: args{
				data: `
				{
					"address": {
						"city": {
							"name": "Kathmandu"
						}
					}
				}`,
				paths: []string{"address.city.name"},
			},
		},
		{
			name: "multiple children on some level",
			args: args{
				data: `
				{
					"address": {
						"city": {
							"name": "Imadol",
							"district": "Lalitpur"
						}
					}
				}`,
				paths: []string{"address.city"},
			},
		},
		{
			name: "multiple children on some level",
			args: args{
				data: `
				{
					"address": {
						"city": {
							"name": "Imadol",
							"district": "Lalitpur",
							"block": {
								"section": "b",
								"name": "Sagarmatha Tol"
							}
						}
					}
				}`,
				paths: []string{"address.city", "address.city.block"},
			},
		},
		{
			name: "multiple top level children",
			args: args{
				data: `
				{
					"metadata": {
						"annotations": {
							"control-plane.alpha.kubernetes.io/leader": "{\"holderIdentity\":\"ip-172-16-56-162.eu-west-2.compute.internal\",\"leaseDurationSeconds\":30,\"acquireTime\":\"2023-03-07T10:13:03Z\",\"renewTime\":\"2023-03-16T05:10:21Z\",\"leaderTransitions\":25}"
						},
						"resourceVersion": "483339358"
					}
				}`,
				paths: []string{
					"metadata.resourceVersion",
					"metadata.annotations.control-plane.alpha.kubernetes.io/leader",
				},
			},
		},
		{
			name: "a child with an array",
			args: args{
				data: `
				{
				  "status": {
				    "conditions": [
				      {
				        "type": "Ready",
				        "reason": "ChartPullFailed",
				        "status": "False",
				        "message": "no chart version found for mysql-8.8.8",
				        "lastTransitionTime": "2023-03-16T04:47:24.000Z"
				      }
				    ]
				  },
				  "metadata": {
				    "resourceVersion": "483324452"
				  }
				}`,
				paths: []string{
					"status.conditions",
					"metadata.resourceVersion",
				},
			},
		},
		{
			name: "deeply nested",
			args: args{
				data: `
				{
				  "a": {
				    "b": {
				      "c": {
				        "d": {
				          "e": {
				            "f": 1,
				            "g": 2
				          }
				        }
				      },
				      "h": 3,
				      "i": {
				        "j": {
				          "k": 4
				        }
				      }
				    }
				  },
				  "metadata": {
				    "resourceVersion": "483324452"
				  }
				}
				`,
				paths: []string{
					"a.b.c.d.e",
					"a.b.h",
					"a.b.i.j.k",
					"metadata.resourceVersion",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m map[string]any
			if err := json.Unmarshal([]byte(tt.args.data), &m); err != nil {
				t.Fatalf("Failed to unmarshal data: %v", err)
			}

			paths := ExtractLeafNodesAndCommonParents(m)
			assert.ElementsMatch(t, tt.args.paths, paths)
		})
	}
}

func TestParseJQ(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expr     string
		expected map[string]any
		wantErr  bool
	}{
		{
			name: "Test recursive removal of fields by key",
			input: map[string]any{
				"etag": "abc123",
				"data": []map[string]any{
					{
						"etag":  "efg",
						"color": "blue",
					},
					{
						"etag":  "efg",
						"color": "red",
					},
				},
				"properties": map[string]any{
					"etag": "abc123",
					"kind": "deployment",
				},
			},
			expr: `walk(if type == "object" then with_entries(select(.key | test("etag") | not)) else . end)`,
			expected: map[string]any{
				"data": []any{
					map[string]any{"color": "blue"},
					map[string]any{"color": "red"},
				},
				"properties": map[string]any{
					"kind": "deployment",
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.Marshal(tt.input)
			if err != nil {
				t.Errorf("ParseJQ() error = %v", err)
				return
			}

			got, err := ParseJQ(b, tt.expr)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseJQ() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				var gotOutput map[string]any
				if err := json.Unmarshal(got.([]byte), &gotOutput); err != nil {
					t.Errorf("ParseJQ() error = %v", err)
					return
				}

				if diff := cmp.Diff(gotOutput, tt.expected); diff != "" {
					t.Errorf("%v", diff)
				}
			}
		})
	}
}
