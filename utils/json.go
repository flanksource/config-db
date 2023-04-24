package utils

import (
	"encoding/json"
)

func StructToJSON(v any) (string, error) {
	b, err := json.Marshal(&v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ToJSONMap takes an input value of struct or map type and converts it to a map[string]any representation
// using JSON encoding and decoding.
func ToJSONMap(s any) (map[string]any, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}

	result := make(map[string]any)
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}

	return result, nil
}
