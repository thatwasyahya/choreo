package auth

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"
)

// --- passwords -------------------------------------------------------------

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("un mot de passe correct")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	ok, rehash, err := VerifyPassword(hash, "un mot de passe correct")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("the correct password was rejected")
	}
	if rehash {
		t.Error("a freshly written hash should not need rehashing")
	}
}

func TestPasswordRejectsWrong(t *testing.T) {
	hash, err := HashPassword("le bon mot de passe")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	ok, _, err := VerifyPassword(hash, "le mauvais mot de passe")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if ok {
		t.Error("a wrong password was accepted")
	}
}

// The salt must be per-hash, or two accounts with the same password would be
// visibly identical in the database.
func TestPasswordSaltIsPerHash(t *testing.T) {
	first, err := HashPassword("identique")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	second, err := HashPassword("identique")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	if first == second {
		t.Error("the same password produced the same hash twice: the salt is not random")
	}
}

// The parameters have to travel inside the hash, otherwise raising them later
// invalidates every existing account.
func TestPasswordHashCarriesItsParameters(t *testing.T) {
	hash, err := HashPassword("peu importe")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("hash does not carry its parameters in PHC form: %s", hash)
	}
}

func TestPasswordAsksForRehashWhenStoredWeaker(t *testing.T) {
	// A hash as a past, weaker configuration would have written it.
	salt := []byte("0123456789abcdef")
	weak := argon2.IDKey([]byte("ancien"), salt, 1, 8192, 1, argonKeyLen)
	stored := "$argon2id$v=19$m=8192,t=1,p=1$" +
		base64.RawStdEncoding.EncodeToString(salt) + "$" +
		base64.RawStdEncoding.EncodeToString(weak)

	ok, rehash, err := VerifyPassword(stored, "ancien")
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("the correct password was rejected against a weaker hash")
	}
	if !rehash {
		t.Error("a weaker stored hash must ask to be rehashed")
	}
}

func TestPasswordRejectsMalformedHash(t *testing.T) {
	for name, hash := range map[string]string{
		"empty":          "",
		"not argon":      "$bcrypt$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		"missing fields": "$argon2id$v=19$m=19456,t=2,p=1$c2FsdA",
		"zero memory":    "$argon2id$v=19$m=0,t=2,p=1$c2FsdA$aGFzaA",
		"bad base64":     "$argon2id$v=19$m=19456,t=2,p=1$!!!!$aGFzaA",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := VerifyPassword(hash, "x"); !errors.Is(err, ErrHashMalformed) {
				t.Errorf("err = %v, want ErrHashMalformed", err)
			}
		})
	}
}

// --- tokens ----------------------------------------------------------------

func TestTokenIsUniqueAndDigestable(t *testing.T) {
	first, firstDigest, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	second, _, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	if first == second {
		t.Error("two tokens came out identical")
	}
	if string(TokenDigest(first)) != string(firstDigest) {
		t.Error("TokenDigest does not reproduce the digest returned at mint time")
	}
	if len(firstDigest) != 32 {
		t.Errorf("digest length = %d, want 32", len(firstDigest))
	}
}

// --- codes -----------------------------------------------------------------

func TestInviteCodeShape(t *testing.T) {
	display, digest, err := NewInviteCode()
	if err != nil {
		t.Fatalf("NewInviteCode: %v", err)
	}

	normalised := NormaliseCode(display)
	if len(normalised) != inviteCodeLen {
		t.Errorf("normalised length = %d, want %d (%q)", len(normalised), inviteCodeLen, display)
	}
	if string(CodeDigest(normalised)) != string(digest) {
		t.Error("a code typed back from its display form does not match its digest")
	}
	if !strings.Contains(display, "-") {
		t.Errorf("display form is not grouped: %q", display)
	}
}

func TestRecoveryCodeShape(t *testing.T) {
	display, digest, err := NewRecoveryCode()
	if err != nil {
		t.Fatalf("NewRecoveryCode: %v", err)
	}

	normalised := NormaliseCode(display)
	if len(normalised) != recoveryCodeLen {
		t.Errorf("normalised length = %d, want %d", len(normalised), recoveryCodeLen)
	}
	if string(CodeDigest(normalised)) != string(digest) {
		t.Error("the recovery code does not survive a round trip through its display form")
	}
}

// The alphabet excludes I, L, O and U so a code read aloud is unambiguous.
func TestCodesAvoidConfusableLetters(t *testing.T) {
	for range 40 {
		display, _, err := NewInviteCode()
		if err != nil {
			t.Fatalf("NewInviteCode: %v", err)
		}
		if i := strings.IndexAny(display, "ILOU"); i >= 0 {
			t.Fatalf("code %q contains a confusable letter at %d", display, i)
		}
	}
}

// What someone types off a scrap of paper has to work.
func TestNormaliseCodeForgivesHumanTyping(t *testing.T) {
	const want = "ABCD1234"

	for name, input := range map[string]string{
		"canonical":   "ABCD1234",
		"lowercase":   "abcd1234",
		"dashes":      "ABCD-1234",
		"spaces":      "ABCD 1234",
		"underscores": "ABCD_1234",
		"I read as 1": "ABCDI234",
		"l read as 1": "abcdl234",
		"O read as 0": "ABCD123O",
		"trailing .":  "ABCD-1234.",
	} {
		t.Run(name, func(t *testing.T) {
			got := NormaliseCode(input)
			if name == "O read as 0" {
				// That one normalises to a different, equally canonical value.
				if got != "ABCD1230" {
					t.Errorf("NormaliseCode(%q) = %q", input, got)
				}
				return
			}
			if got != want {
				t.Errorf("NormaliseCode(%q) = %q, want %q", input, got, want)
			}
		})
	}
}

// --- rate limiting ---------------------------------------------------------

func TestLimiterAllowsBurstThenRefuses(t *testing.T) {
	l := NewLimiter(3, time.Minute)

	for i := range 3 {
		if !l.Allow("yahya") {
			t.Fatalf("attempt %d was refused inside the burst", i+1)
		}
	}
	if l.Allow("yahya") {
		t.Error("the fourth attempt was allowed past a burst of three")
	}
}

func TestLimiterIsPerKey(t *testing.T) {
	l := NewLimiter(1, time.Minute)

	if !l.Allow("camille") {
		t.Fatal("first key refused")
	}
	if !l.Allow("jordan") {
		t.Error("one key's budget was charged to another")
	}
}

func TestLimiterRefillsOverTime(t *testing.T) {
	clock := time.Now()
	l := NewLimiter(2, time.Minute)
	l.now = func() time.Time { return clock }

	// Two separate statements: with || the short-circuit makes the intent
	// unreadable, and both calls have side effects.
	if !l.Allow("k") {
		t.Fatal("the first of the burst was refused")
	}
	if !l.Allow("k") {
		t.Fatal("the second of the burst was refused")
	}
	if l.Allow("k") {
		t.Fatal("the burst was not exhausted")
	}

	// Half a minute buys one of the two tokens back.
	clock = clock.Add(30 * time.Second)
	if !l.Allow("k") {
		t.Error("no token was refilled after half the window")
	}
	if l.Allow("k") {
		t.Error("more was refilled than the elapsed time allows")
	}
}

// --- validation ------------------------------------------------------------

func TestValidateLogin(t *testing.T) {
	for name, login := range map[string]string{
		"plain":       "yahya",
		"with dot":    "yahya.e",
		"with dash":   "yahya-e",
		"underscore":  "yahya_e",
		"digit start": "1yahya",
		"minimum":     "abc",
	} {
		t.Run("accepts "+name, func(t *testing.T) {
			if err := ValidateLogin(login); err != nil {
				t.Errorf("ValidateLogin(%q) = %v, want nil", login, err)
			}
		})
	}

	for name, tc := range map[string]struct {
		login string
		want  error
	}{
		"too short":   {"ab", ErrLoginLength},
		"too long":    {strings.Repeat("a", 33), ErrLoginLength},
		"leading dot": {".yahya", ErrLoginStart},
		"space":       {"ya hya", ErrLoginCharset},
		"at sign":     {"yahya@example", ErrLoginCharset},
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			if err := ValidateLogin(tc.login); !errors.Is(err, tc.want) {
				t.Errorf("ValidateLogin(%q) = %v, want %v", tc.login, err, tc.want)
			}
		})
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("dix caract"); err != nil {
		t.Errorf("a ten-character password was refused: %v", err)
	}
	if err := ValidatePassword("neuf car."); !errors.Is(err, ErrPasswordShort) {
		t.Errorf("err = %v, want ErrPasswordShort", err)
	}
	// No composition rules: a long passphrase with no symbol is fine.
	if err := ValidatePassword("cheval batterie agrafe correct"); err != nil {
		t.Errorf("a passphrase was refused: %v", err)
	}
}
