package utils

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func Hash(v interface{}) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}

	hash := md5.Sum(data)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash[:]), nil
}

func Sha256Hex(in string) string {
	hash := sha256.New()
	hash.Write([]byte(in))
	hashVal := hash.Sum(nil)
	return hex.EncodeToString(hashVal[:])
}
