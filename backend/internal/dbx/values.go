package dbx

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
)

var jsonNumberPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)

// JSON numbers must reach the driver before a float64 conversion can round a
// primary key or decimal. Numeric strings bind unchanged; the column determines
// their SQL type. This also preserves unsigned integers larger than MaxInt64.
func SQLValue(value any) (any, error) {
	switch v := value.(type) {
	case json.Number:
		// Parsing through float64 would reject valid database numerics outside
		// its exponent range and can discard digits before the driver sees them.
		if !jsonNumberPattern.MatchString(v.String()) {
			return nil, fmt.Errorf("invalid numeric value")
		}
		return v.String(), nil
	case uint64:
		return strconv.FormatUint(v, 10), nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("numeric values must be finite")
		}
		if math.Trunc(v) == v && math.Abs(v) > 9007199254740991 {
			return nil, fmt.Errorf("large integers must be decimal strings")
		}
	case float32:
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, fmt.Errorf("numeric values must be finite")
		}
	}
	return value, nil
}

func sqlArguments(args []any) ([]any, error) {
	values := make([]any, len(args))
	for i, arg := range args {
		value, err := SQLValue(arg)
		if err != nil {
			return nil, fmt.Errorf("argument %d: %w", i+1, err)
		}
		values[i] = value
	}
	return values, nil
}

// ClickHouse can return typed arrays/maps containing Int64, UInt64 or big.Int.
// Protect their elements too: only converting top-level cells would still let
// JSON.parse round a nested identifier. Driver scalar Stringers (notably big.Int
// and decimal wrappers) retain their exact textual representation.
func normaliseNumericContainer(value any) any {
	if integer, ok := value.(big.Int); ok {
		return integer.String()
	}
	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return nil
	}
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		if text, ok := value.(fmt.Stringer); ok {
			return text.String()
		}
		return normaliseValue(rv.Elem().Interface())
	}
	if text, ok := value.(fmt.Stringer); ok {
		return text.String()
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return nil
		}
		items := make([]any, rv.Len())
		for i := range items {
			items[i] = normaliseValue(rv.Index(i).Interface())
		}
		return items
	case reflect.Map:
		if rv.IsNil() {
			return nil
		}
		keyKind := rv.Type().Key().Kind()
		if keyKind != reflect.String && !(keyKind >= reflect.Int && keyKind <= reflect.Uint64) {
			return value
		}
		items := make(map[string]any, rv.Len())
		for _, key := range rv.MapKeys() {
			items[fmt.Sprint(key.Interface())] = normaliseValue(rv.MapIndex(key).Interface())
		}
		return items
	}
	return value
}
