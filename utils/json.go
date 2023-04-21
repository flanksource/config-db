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

func StructToMap(s any) (map[string]any, error) {
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

// func StructToMap(s any) (map[string]string, error) {
// 	raw, err := json.Marshal(s)
// 	if err != nil {
// 		return nil, err
// 	}

// 	result := make(map[string]interface{})
// 	if err := json.Unmarshal(raw, &result); err != nil {
// 		return nil, err
// 	}

// 	output := make(map[string]string)
// 	for k, v := range result {
// 		switch val := v.(type) {
// 		case string:
// 			output[k] = val
// 		default:
// 			a, err := utils.Stringify(val)
// 			if err != nil {
// 				return nil, err
// 			}

// 			output[k] = a
// 		}
// 	}

// 	return output, nil
// }
