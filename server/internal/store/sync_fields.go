package store

import (
	"encoding/json"
	"fmt"
	"math"
)

// column is a writable column: where a logical field lands, what the database
// will accept there, and how much of it.
//
// The limit travels WITH the column because it is the column CHECK constraint
// restated. A CHECK that fires aborts the whole transaction, which comes back
// to the phone as a 500 — our problem — when an over-long label is theirs, and
// is answerable with a refusal. Keeping the bound here, beside the cast, is
// what makes it impossible to add a bounded column and forget to say so.
type column struct {
	name  string
	cast  string
	limit int // characters; zero where the column carries no CHECK
}

// taskColumn maps a logical field name to its column.
//
// Unknown names are NOT an error: they are kept in the row ext column. That is
// what lets a field added by a newer app survive a write against an older
// server, and it is half of what makes N-2 compatibility free. The other half
// is that a row state travelling down carries fields the receiving app may not
// know, and it applies the row anyway.
//
// A switch and not a package-level map, so there is no mutable global and the
// list of writable columns is impossible to extend by accident.
func taskColumn(field string) (column, bool) {
	switch field {
	case "title":
		return column{name: "title", cast: "text", limit: 500}, true
	case "notes":
		return column{name: "notes", cast: "text", limit: 8000}, true
	case "due_on":
		return column{name: "due_on", cast: "date"}, true
	case "due_time":
		return column{name: "due_time", cast: "time"}, true
	case "all_day":
		return column{name: "all_day", cast: "boolean"}, true
	case "assignee_id":
		return column{name: "assignee_id", cast: "uuid"}, true
	case "category_id":
		return column{name: "category_id", cast: "uuid"}, true
	case "priority":
		return column{name: "priority", cast: "smallint"}, true
	default:
		// kind is deliberately absent: a task does not become an event by a
		// field write. It is set once, at creation.
		return column{}, false
	}
}

// coerce turns a value that came out of JSON into something the column accepts.
//
// JSON has one number type, so an integer column receives a float64 and a
// smallint receives one that may not be an integer at all. Refusing that here,
// where the field name is still known, gives a precise malformed rather than a
// driver error three layers down.
func coerce(col column, value any) (any, error) {
	if value == nil {
		return nil, nil
	}

	switch col.cast {
	case "smallint":
		number, ok := value.(float64)
		if !ok {
			return nil, fmt.Errorf("%w: expected a number, got %T", errFieldType, value)
		}
		if number != math.Trunc(number) || number < math.MinInt16 || number > math.MaxInt16 {
			return nil, fmt.Errorf("%w: %v is not a small integer", errFieldType, number)
		}
		return int16(number), nil

	case "numeric":
		// A quantity: two eggs, 1.5 litres. Fractional on purpose — half the
		// entries on a shopping list are not whole things.
		number, ok := value.(float64)
		if !ok {
			return nil, fmt.Errorf("%w: expected a number, got %T", errFieldType, value)
		}
		if math.IsInf(number, 0) || math.IsNaN(number) {
			// PostgreSQL numeric has NaN but no infinity, and neither is a
			// quantity of anything.
			return nil, fmt.Errorf("%w: %v is not a quantity", errFieldType, number)
		}
		return number, nil

	case "boolean":
		flag, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("%w: expected a boolean, got %T", errFieldType, value)
		}
		return flag, nil

	case "text", "date", "time", "uuid":
		// The cast in the statement does the parsing, and PostgreSQL rejects a
		// malformed date far more precisely than we would.
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%w: expected a string, got %T", errFieldType, value)
		}
		// Characters, not bytes: length() counts characters, so a name of 200
		// accented letters has to pass here exactly as it passes there.
		if col.limit > 0 && len([]rune(text)) > col.limit {
			return nil, fmt.Errorf("%w: %s is longer than %d characters",
				errFieldType, col.name, col.limit)
		}
		return text, nil

	default:
		return nil, fmt.Errorf("%w: unknown column type %q", errFieldType, col.cast)
	}
}

// jsonOf encodes a map for a jsonb parameter.
func jsonOf(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("store: encoding jsonb: %w", err)
	}
	return encoded, nil
}
