package utils

import "encoding/json"

func CloneWithJSON[T any](v T) (T, error) {
	b, err := json.Marshal(&v)
	if err != nil {
		return v, err
	}

	var v2 T
	return v2, json.Unmarshal(b, &v2)
}
