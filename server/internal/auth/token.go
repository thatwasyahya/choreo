package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
)

// tokenBytes is the raw entropy behind an access or refresh token.
const tokenBytes = 32

// NewToken mints an opaque token and returns both the value handed to the
// client and the digest to store.
//
// sha256 and not argon2id: thirty-two random bytes have nothing to brute-force,
// so there is nothing to slow down. A password does, which is why that one goes
// through argon2id.
func NewToken() (token string, digest []byte, err error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("auth: reading token bytes: %w", err)
	}

	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, TokenDigest(token), nil
}

// TokenDigest returns the digest stored for a token.
func TokenDigest(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// Crockford base32: no I, L, O or U. So 1 is never read as I, 0 never as O, and
// no accidental obscenity comes out of a random draw.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Code lengths, in characters. Each character carries exactly 5 bits.
const (
	inviteCodeLen   = 10 // 50 bits
	recoveryCodeLen = 24 // 120 bits
)

// NewInviteCode mints a code a person can read out over the phone.
//
// Fifty bits is guessable in theory. In practice the code is single-use, expires
// in 48 hours, and redeeming it requires already being authenticated and passes
// through a bucket of ten per hour — so there is no anonymous brute force to be
// had.
func NewInviteCode() (display string, digest []byte, err error) {
	return newCode(inviteCodeLen)
}

// NewRecoveryCode mints the code shown once, at sign-up.
//
// It is the only safety net when there is no e-mail address, so it is sized to
// be the strongest credential in the system rather than the weakest.
func NewRecoveryCode() (display string, digest []byte, err error) {
	return newCode(recoveryCodeLen)
}

func newCode(length int) (display string, digest []byte, err error) {
	raw := make([]byte, length)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("auth: reading code bytes: %w", err)
	}

	var plain strings.Builder
	plain.Grow(length)
	for _, b := range raw {
		// The low five bits of a uniform byte are themselves uniform over
		// 0..31, so there is no modulo bias to correct.
		plain.WriteByte(crockford[b&0x1f])
	}

	normalised := plain.String()
	return group(normalised), CodeDigest(normalised), nil
}

// CodeDigest returns the digest stored for a code. The caller must pass it
// through NormaliseCode first, or an equivalent code typed with dashes will not
// match.
func CodeDigest(normalised string) []byte {
	sum := sha256.Sum256([]byte(normalised))
	return sum[:]
}

// NormaliseCode turns what a human typed into the canonical form.
//
// It accepts any case, any spacing, any dashes, and forgives the three
// substitutions people actually make when copying from paper: I and L read as 1,
// O read as 0. Those letters are absent from the alphabet, so accepting them
// costs no ambiguity.
func NormaliseCode(input string) string {
	var out strings.Builder
	out.Grow(len(input))

	for _, r := range strings.ToUpper(input) {
		switch r {
		case ' ', '-', '\t', '_':
			continue
		case 'I', 'L':
			out.WriteByte('1')
		case 'O':
			out.WriteByte('0')
		default:
			if strings.ContainsRune(crockford, r) {
				out.WriteRune(r)
			}
			// Anything else is dropped: a code is only ever these 32 symbols,
			// and silently ignoring a stray character beats rejecting a
			// correct code because of a trailing full stop.
		}
	}
	return out.String()
}

// group inserts a dash every four characters, which is how people read long
// strings without losing their place.
func group(code string) string {
	var out strings.Builder
	for i, r := range code {
		if i > 0 && i%4 == 0 {
			out.WriteByte('-')
		}
		out.WriteRune(r)
	}
	return out.String()
}
