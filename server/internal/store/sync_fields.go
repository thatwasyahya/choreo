package store

import (
	"encoding/json"
	"fmt"
	"math"
)

// taskColumn maps a logical field name to its column and SQL type.
//
// Unknown names are NOT an error: they are kept in the row's ext column. That
// is what lets a field added by a newer app survive a write against an older
// server, and it is half of what makes N-2 compatibility free. The other half
// is that a row state travelling down carries fields the receiving app may not
// know, and it applies the row anyway.
//
// A switch and not a package-level map, so there is no mutable global and the
// list of writable columns is impossible to extend by accident.
func taskColumn(field string) (column, cast string, ok bool) {
	switch field {
	case "title":
		return "title", "text", true
	case "notes":
		return "notes", "text", true
	case "due_on":
		return "due_on", "date", true
	case "due_time":
		return "due_time", "time", true
	case "all_day":
		return "all_day", "boolean", true
	case "assignee_id":
		return "assignee_id", "uuid", true
	case "category_id":
		return "category_id", "uuid", true
	case "priority":
		return "priority", "smallint", true
	default:
		// kind is deliberately absent: a task does not become an event by a
		// field write. It is set once, at creation.
		return "", "", false
	}
}

// coerce turns a value that came out of JSON into something the column will
// accept.
//
// JSON has one number type, so an integer column receives a float64 and a
// smallint receives one that may not be an integer at all. Refusing that here,
// where the field name is still known, gives a precise malformed rather than a
// driver error three layers down.
func coerce(cast string, value any) (any, error) {
	if value == nil {
		return nil, nil
	}

	switch cast {
	case "smallint":
		number, ok := value.(float64)
		if !ok {
			return nil, fmt.Errorf("%w: expected a number, got %T", errFieldType, value)
		}
		if number != math.Trunc(number) || number < math.MinInt16 || number > math.MaxInt16 {
			return nil, fmt.Errorf("%w: %v is not a small integer", errFieldType, number)
		}
		return int16(number), nil

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
		return text, nil

	default:
		return nil, fmt.Errorf("%w: unknown column type %q", errFieldType, cast)
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
