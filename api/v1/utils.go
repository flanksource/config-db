package v1

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"strings"
)

func IsJSONPath(path string) bool {
	return strings.HasPrefix(path, "$") || strings.HasPrefix(path, "@")
}

// Hash returns the MD5 hash of the JSON representation of the given value.
func Hash(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}

	hash := md5.Sum(data)
	return hex.EncodeToString(hash[:]), nil
}
