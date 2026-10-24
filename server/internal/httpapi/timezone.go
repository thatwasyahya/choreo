package httpapi

import (
	"errors"
	"fmt"
	"strings"
	"time"

	// The timezone database, embedded in the binary.
	//
	// The server runs from a distroless image: no shell, no package manager and
	// no /usr/share/zoneinfo. Without this import LoadLocation would resolve
	// every zone on a development machine and NONE in production — a validator
	// that passes every test and rejects every real household. 450 kB is a
	// cheap price for that failure not existing.
	//
	// Imported here, in the package that calls LoadLocation, rather than in
	// main: a binary that links this package gets the data, and the tests
	// exercise the same lookup the server will.
	_ "time/tzdata"
)

// ErrInvalidTimeZone rejects anything that is not an IANA zone name.
var ErrInvalidTimeZone = errors.New("httpapi: tz must be an IANA zone name")

// validateTimeZone checks a household's time zone.
//
// It has to be an IANA name — "Europe/Paris" — and never an abbreviation. The
// difference is not cosmetic: "CEST" is central European SUMMER time, a fixed
// +02:00 with no notion of a transition, so a household carrying it would see
// every date in the winter half of the year shift by an hour. "Bin day is
// Tuesday" would become Monday at 23:00, which is exactly the failure the
// specification avoids by putting the zone on the HOUSEHOLD and storing dates
// rather than instants.
//
// The first client to create a household sent "CEST", because that is what
// Android's abbreviation getter returns. The client is fixed too, but the
// server is the only place that can promise the column means something: a
// value no client may write is a value no client can write.
func validateTimeZone(tz string) error {
	if strings.TrimSpace(tz) == "" {
		return fmt.Errorf("%w (empty)", ErrInvalidTimeZone)
	}

	// "Local" resolves — to the SERVER's zone, which is meaningless for a
	// household and would silently follow a redeploy to another machine.
	if tz == "Local" {
		return fmt.Errorf("%w (got %q)", ErrInvalidTimeZone, tz)
	}

	// An abbreviation, an offset or a made-up name all fail here. LoadLocation
	// accepts a name only if the embedded database has it.
	if _, err := time.LoadLocation(tz); err != nil {
		return fmt.Errorf("%w (got %q)", ErrInvalidTimeZone, tz)
	}

	// A zone whose name carries no region is either an abbreviation that the
	// database happens to know or a legacy alias. Requiring a slash keeps the
	// column to one shape, with UTC as the one exception worth making: it is
	// the honest answer for a household that does not want a local zone.
	if tz != "UTC" && !strings.Contains(tz, "/") {
		return fmt.Errorf("%w (got %q)", ErrInvalidTimeZone, tz)
	}

	return nil
}
