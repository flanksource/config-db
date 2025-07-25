package utils

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"strings"
)

func Hash(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}

	hash := md5.Sum(data)
	return hex.EncodeToString(hash[:]), nil
}

func Sha256Hex(in string) string {
	hash := sha256.New()
	hash.Write([]byte(in))
	hashVal := hash.Sum(nil)
	return hex.EncodeToString(hashVal[:])
}

func Base32ToString(input string) (string, error) {
	upperInput := strings.ToUpper(input)

	// Calculate padding needed
	paddingNeeded := (8 - len(input)%8) % 8
	paddedInput := upperInput + strings.Repeat("=", paddingNeeded)

	decoded, err := base32.StdEncoding.DecodeString(paddedInput)
	if err != nil {
		return "", err
	}

	hexString := hex.EncodeToString(decoded)
	return hexString, nil
}
