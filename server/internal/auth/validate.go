package auth

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

// Errors a caller is expected to map onto a status code. See docs/AUTH.md.
var (
	ErrLoginLength   = errors.New("auth: login must be 3 to 32 characters")
	ErrLoginCharset  = errors.New("auth: login may only contain letters, digits, . - and _")
	ErrLoginStart    = errors.New("auth: login must start with a letter or a digit")
	ErrPasswordShort = errors.New("auth: password must be at least 10 characters")
)

const (
	loginMin    = 3
	loginMax    = 32
	passwordMin = 10
)

// ValidateLogin checks a chosen login.
//
// Comparison and uniqueness are case-insensitive in the database (citext), so
// nothing is lowercased here: someone who types "Yahya" keeps their capital.
func ValidateLogin(login string) error {
	n := utf8.RuneCountInString(login)
	if n < loginMin || n > loginMax {
		return fmt.Errorf("%w (got %d)", ErrLoginLength, n)
	}

	for i, r := range login {
		alnum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')

		if i == 0 && !alnum {
			return ErrLoginStart
		}
		if !alnum && r != '.' && r != '-' && r != '_' {
			return ErrLoginCharset
		}
	}
	return nil
}

// ValidatePassword enforces a length floor and nothing else.
//
// No composition rules, on purpose: demanding an uppercase, a digit and a
// symbol produces "Password1!" and not one bit more of real entropy. Length is
// the only requirement that survives contact with people.
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < passwordMin {
		return ErrPasswordShort
	}
	return nil
}
