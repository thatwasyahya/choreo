// Package auth holds the credential primitives: password hashing, opaque
// tokens, the codes people read out loud, and rate limiting.
//
// It knows nothing about HTTP or about PostgreSQL. See docs/AUTH.md for the
// decisions this implements, and why tokens here are opaque rather than JWT.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// The OWASP minimum configuration for argon2id. 19 MiB per computation sits
// comfortably in the 4 GB of the target VPS even with several logins at once,
// where 64 MiB would expose the server to memory exhaustion by simply repeating
// connection attempts.
const (
	argonMemory  = 19456 // KiB
	argonTime    = 2
	argonThreads = 1
	argonSaltLen = 16
	argonKeyLen  = 32
)

// ErrHashMalformed is returned when a stored hash cannot be parsed. It means
// the database holds something this code did not write.
var ErrHashMalformed = errors.New("auth: malformed password hash")

// HashPassword returns a PHC-format argon2id hash.
//
// The parameters travel inside the string, so raising them later invalidates no
// existing account: the hash is simply recomputed on the next successful login.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: reading salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt,
		argonTime, argonMemory, argonThreads, argonKeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches hash.
//
// needsRehash is true when the stored hash used weaker parameters than the
// current ones, which is the caller's cue to re-hash and store it.
func VerifyPassword(hash, password string) (ok bool, needsRehash bool, err error) {
	params, salt, want, err := parsePHC(hash)
	if err != nil {
		return false, false, err
	}

	// parsePHC refuses anything outside keyLenMin..keyLenMax, so this cannot
	// overflow.
	keyLen := uint32(len(want)) //nolint:gosec // bounded by parsePHC

	got := argon2.IDKey([]byte(password), salt,
		params.time, params.memory, params.threads, keyLen)

	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false, nil
	}

	weaker := params.memory < argonMemory ||
		params.time < argonTime ||
		len(want) < argonKeyLen
	return true, weaker, nil
}

// SpendVerifyBudget performs the same work as VerifyPassword on a throwaway
// hash.
//
// It exists for the case where the login does not exist. Answering instantly
// there, and slowly when it does, tells an attacker exactly which logins are
// real — so the server pays the same cost either way.
func SpendVerifyBudget() {
	// A fixed salt is fine: nothing here is ever stored or compared.
	salt := make([]byte, argonSaltLen)
	_ = argon2.IDKey([]byte("budget"), salt,
		argonTime, argonMemory, argonThreads, argonKeyLen)
}

// Bounds on a stored key length. They exist so the conversion in
// VerifyPassword is provably safe, and so a corrupt row cannot ask argon2 for
// something absurd.
const (
	keyLenMin = 16
	keyLenMax = 64
)

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func parsePHC(hash string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(hash, "$")
	// "", "argon2id", "v=19", "m=…,t=…,p=…", salt, key
	if len(parts) != 6 || parts[1] != "argon2id" {
		return argonParams{}, nil, nil, ErrHashMalformed
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return argonParams{}, nil, nil, ErrHashMalformed
	}

	var params argonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d",
		&params.memory, &params.time, &params.threads); err != nil {
		return argonParams{}, nil, nil, ErrHashMalformed
	}
	if params.memory == 0 || params.time == 0 || params.threads == 0 {
		return argonParams{}, nil, nil, ErrHashMalformed
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return argonParams{}, nil, nil, ErrHashMalformed
	}

	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) < keyLenMin || len(key) > keyLenMax {
		return argonParams{}, nil, nil, ErrHashMalformed
	}

	return params, salt, key, nil
}
