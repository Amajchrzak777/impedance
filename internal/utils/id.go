package utils

import (
	"crypto/rand"
	"encoding/hex"
)

// GenerateID generates a unique ID for requests
func GenerateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}
