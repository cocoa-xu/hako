// Package identity provides stable UUIDs for worker installations.
package identity

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"strings"
)

// New returns a randomly generated UUID version 4.
func New() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return format(id[:])
}

func format(id []byte) string {
	text := hex.EncodeToString(id)
	return text[:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:]
}

// Valid recognizes the canonical UUID forms used by Hako.
func Valid(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	hexID := strings.ReplaceAll(id, "-", "")
	return len(hexID) == 32 && strings.Trim(hexID, "0123456789abcdef") == "" && (id[14] == '4' || id[14] == '5') && strings.ContainsRune("89ab", rune(id[19]))
}

// Normalize upgrades pre-release installation IDs to deterministic UUID version 5.
func Normalize(id string) string {
	if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
		return id
	}
	namespace, _ := hex.DecodeString("6ba7b8109dad11d180b400c04fd430c8")
	digest := sha1.Sum(append(namespace, []byte("moe.uwucocoa.hako/device/"+id)...))
	digest[6] = (digest[6] & 0x0f) | 0x50
	digest[8] = (digest[8] & 0x3f) | 0x80
	return format(digest[:16])
}
