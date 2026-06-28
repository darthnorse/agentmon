package authn

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory  = 64 * 1024 // KiB
	argonTime    = 3
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// HashPassword returns a PHC-formatted argon2id hash with a random salt.
func HashPassword(plain string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(plain), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether plain matches the PHC-encoded argon2id hash.
// It returns (false, nil) for a wrong password and an error only when encoded is
// malformed. The final comparison is constant-time.
func VerifyPassword(encoded, plain string) (bool, error) {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, fmt.Errorf("bad argon2id encoding")
	}
	var version, mem, time, threads int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, fmt.Errorf("bad version: %w", err)
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &time, &threads); err != nil {
		return false, fmt.Errorf("bad params: %w", err)
	}
	if version != argon2.Version {
		return false, fmt.Errorf("unsupported argon2 version %d", version)
	}
	if mem <= 0 || mem > 1<<21 || time <= 0 || time > 16 || threads <= 0 || threads > 16 {
		return false, fmt.Errorf("argon2 params out of range")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, fmt.Errorf("bad salt: %w", err)
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, fmt.Errorf("bad hash: %w", err)
	}
	got := argon2.IDKey([]byte(plain), salt, uint32(time), uint32(mem), uint8(threads), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
