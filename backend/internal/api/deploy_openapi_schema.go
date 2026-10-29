package api

import (
	"encoding"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The schemas in the deployment API's OpenAPI description are read off the Go
// types the handlers decode and encode, by reflection, following the rules
// encoding/json applies to them: the `json` tag names a field and `-` hides
// it, `omitempty` makes it optional, an embedded struct's fields are the outer
// struct's, and a pointer, slice or map may be null. Nothing here is written
// by hand for a type, so a field added to a struct is in the description the
// next time it is generated, and a type that is renamed is renamed here. What
// reflection cannot see — which string type is a closed vocabulary, which
// field is a secret written and never read back — is in the two tables
// openapiEnums and openapiWriteOnly, beside the types they describe.

// openapiSchemas collects the component schemas of one document.
type openapiSchemas struct {
	components map[string]map[string]any
	names      map[reflect.Type]string
	started    map[reflect.Type]bool
	// scalars is every named scalar type a schema reached, for the test that
	// checks each closed vocabulary among them is in openapiEnums.
	scalars map[reflect.Type]bool
	// writeOnlyApplied is every line of openapiWriteOnly a field matched,
	// for the test that checks no line names a field that is not there.
	writeOnlyApplied map[string]bool
}

func newOpenAPISchemas() *openapiSchemas {
	return &openapiSchemas{
		components:       map[string]map[string]any{},
		names:            map[reflect.Type]string{},
		started:          map[reflect.Type]bool{},
		scalars:          map[reflect.Type]bool{},
		writeOnlyApplied: map[string]bool{},
	}
}

var (
	timeType          = reflect.TypeOf(time.Time{})
	durationType      = reflect.TypeOf(time.Duration(0))
	rawMessageType    = reflect.TypeOf(json.RawMessage{})
	jsonMarshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
)

// schemaOf is the JSON Schema for a Go type as encoding/json writes and reads
// it. A named struct becomes a component and is referenced; everything else is
// inline.
func (s *openapiSchemas) schemaOf(t reflect.Type) map[string]any {
	// A pointer is its element, which may be null; what the element is comes
	// after, so *time.Time is a nullable date-time rather than a pointer that
	// happens to marshal as text.
	if t.Kind() == reflect.Pointer {
		return nullable(s.schemaOf(t.Elem()))
	}
	switch t {
	case timeType:
		return map[string]any{"type": "string", "format": "date-time"}
	case durationType:
		return map[string]any{"type": "integer", "format": "int64", "description": "A duration in nanoseconds, as Go's time.Duration is encoded."}
	case rawMessageType:
		return map[string]any{"description": "Any JSON value; the handler passes it on as it was sent."}
	}
	if t.Kind() == reflect.Struct && (t.Implements(jsonMarshalerType) || reflect.PointerTo(t).Implements(jsonMarshalerType)) {
		return map[string]any{"description": "A value with a JSON encoding of its own (Go type " + t.String() + ")."}
	}
	if t.Kind() != reflect.String && t.Kind() != reflect.Struct && (t.Implements(textMarshalerType) || reflect.PointerTo(t).Implements(textMarshalerType)) {
		return map[string]any{"type": "string", "description": "Written as text (Go type " + t.String() + ")."}
	}
	switch t.Kind() {
	case reflect.Interface:
		return map[string]any{"description": "Any JSON value."}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64, reflect.Uint, reflect.Uint64, reflect.Uint32:
		return s.enumerated(t, map[string]any{"type": "integer", "format": "int64"})
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Uint8, reflect.Uint16:
		return s.enumerated(t, map[string]any{"type": "integer", "format": "int32"})
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.String:
		return s.enumerated(t, map[string]any{"type": "string"})
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return nullable(map[string]any{"type": "string", "contentEncoding": "base64"})
		}
		return nullable(map[string]any{"type": "array", "items": s.schemaOf(t.Elem())})
	case reflect.Array:
		return map[string]any{"type": "array", "items": s.schemaOf(t.Elem()), "minItems": t.Len(), "maxItems": t.Len()}
	case reflect.Map:
		// encoding/json writes every key as a string, whatever its Go type.
		return nullable(map[string]any{"type": "object", "additionalProperties": s.schemaOf(t.Elem())})
	case reflect.Struct:
		if t.Name() == "" {
			return s.structSchema(t)
		}
		return s.component(t)
	}
	return map[string]any{"description": "A value of Go type " + t.String() + ", which this generator does not describe."}
}

// enumerated adds the closed vocabulary of a named scalar type, when the table
// has one. A type with a custom encoding that is not in the table is still
// described by its underlying kind, which is what the types here encode as.
func (s *openapiSchemas) enumerated(t reflect.Type, base map[string]any) map[string]any {
	if t.Name() != "" {
		s.scalars[t] = true
	}
	if values, ok := openapiEnums[t]; ok {
		list := make([]any, 0, len(values))
		for _, v := range values {
			list = append(list, v)
		}
		base["enum"] = list
	}
	return base
}

// component returns a $ref to a named struct, describing it once.
func (s *openapiSchemas) component(t reflect.Type) map[string]any {
	name, ok := s.names[t]
	if !ok {
		name = componentName(t)
		// Two different types can share a short name only across packages,
		// and the package is in the name, so a clash is a generic's brackets
		// or an unexported twin: number them.
		base, n := name, 1
		for {
			if other, taken := s.componentOwner(name); !taken || other == t {
				break
			}
			n++
			name = fmt.Sprintf("%s_%d", base, n)
		}
		s.names[t] = name
	}
	if !s.started[t] {
		s.started[t] = true
		// Reserve the name before descending, so a type that refers to
		// itself is a $ref, not a loop.
		s.components[name] = nil
		s.components[name] = s.structSchema(t)
	}
	return map[string]any{"$ref": "#/components/schemas/" + name}
}

func (s *openapiSchemas) componentOwner(name string) (reflect.Type, bool) {
	for t, n := range s.names {
		if n == name {
			return t, true
		}
	}
	return nil, false
}

// componentName is pkg.Type, as the Go source names it, with the characters
// OpenAPI does not allow in a component name replaced.
func componentName(t reflect.Type) string {
	pkg := t.PkgPath()
	if i := strings.LastIndex(pkg, "/"); i >= 0 {
		pkg = pkg[i+1:]
	}
	name := t.Name()
	if pkg != "" {
		name = pkg + "." + name
	}
	var sb strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			sb.WriteRune(r)
		default:
			sb.WriteRune('_')
		}
	}
	return sb.String()
}

// structSchema describes a struct's fields as encoding/json writes them.
func (s *openapiSchemas) structSchema(t reflect.Type) map[string]any {
	props := map[string]any{}
	var writeOnly []string
	s.addFields(t, props, &writeOnly, t)
	schema := map[string]any{"type": "object", "properties": props}
	if title := t.Name(); title != "" {
		schema["title"] = componentName(t)
	}
	return schema
}

// addFields adds a struct's JSON fields to props, descending into embedded
// structs the way encoding/json promotes their fields. owner is the struct the
// write-only table is read for.
func (s *openapiSchemas) addFields(t reflect.Type, props map[string]any, writeOnly *[]string, owner reflect.Type) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				s.addFields(ft, props, writeOnly, ft)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		var field map[string]any
		if strings.Contains(","+opts+",", ",string,") {
			field = map[string]any{"type": "string", "description": "A " + f.Type.Kind().String() + " written as a JSON string."}
		} else {
			field = s.schemaOf(f.Type)
		}
		// A $ref may carry no siblings a reader must honour, so a note on a
		// reference wraps it.
		if _, isRef := field["$ref"]; isRef {
			field = map[string]any{"allOf": []any{field}}
		}
		if key := componentName(owner) + "." + f.Name; openapiWriteOnly[key] {
			s.writeOnlyApplied[key] = true
			field["writeOnly"] = true
			field["description"] = joinDescription(field["description"], "A secret: it is accepted here and never read back by any route.")
		}
		props[name] = field
	}
}

func joinDescription(existing any, more string) string {
	if text, ok := existing.(string); ok && text != "" {
		return text + " " + more
	}
	return more
}

// nullable marks a schema as also accepting null, as Go writes a nil pointer,
// slice or map, in the form OpenAPI 3.1 (JSON Schema 2020-12) spells it.
func nullable(schema map[string]any) map[string]any {
	if _, isRef := schema["$ref"]; isRef {
		return map[string]any{"oneOf": []any{schema, map[string]any{"type": "null"}}}
	}
	switch typ := schema["type"].(type) {
	case string:
		schema["type"] = []any{typ, "null"}
	case []any:
		for _, one := range typ {
			if one == "null" {
				return schema
			}
		}
		schema["type"] = append(typ, "null")
	}
	// A schema without a type (any JSON value) already admits null.
	return schema
}

// sortedKeys is a map's keys in order, for output that is the same every time.
func openapiSortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// writeYAML writes a JSON-shaped value (maps, slices, strings, numbers,
// booleans and null, as encoding/json decodes into any) as YAML. Every string
// is a double-quoted scalar, which YAML reads exactly as JSON does, so no
// value is mistaken for a number, a boolean or null, and a multi-line text
// stays one value.
func writeYAML(w io.Writer, v any, indent int) {
	writeYAMLValue(w, v, indent, false)
}

func writeYAMLValue(w io.Writer, v any, indent int, inList bool) {
	pad := strings.Repeat("  ", indent)
	switch value := v.(type) {
	case map[string]any:
		if len(value) == 0 {
			fmt.Fprint(w, " {}\n")
			return
		}
		keys := openapiSortedKeys(value)
		for i, k := range keys {
			if i > 0 || !inList {
				fmt.Fprint(w, pad)
			}
			fmt.Fprint(w, strconv.Quote(k)+":")
			writeYAMLChild(w, value[k], indent)
		}
	case []any:
		if len(value) == 0 {
			fmt.Fprint(w, " []\n")
			return
		}
		for i, item := range value {
			if i > 0 || !inList {
				fmt.Fprint(w, pad)
			}
			fmt.Fprint(w, "- ")
			switch nested := item.(type) {
			case map[string]any:
				if len(nested) == 0 {
					fmt.Fprintln(w, "{}")
				} else {
					writeYAMLValue(w, item, indent+1, true)
				}
			case []any:
				if len(nested) == 0 {
					fmt.Fprintln(w, "[]")
				} else {
					writeYAMLValue(w, item, indent+1, true)
				}
			default:
				fmt.Fprintln(w, yamlScalar(item))
			}
		}
	default:
		fmt.Fprintln(w, yamlScalar(value))
	}
}

// writeYAMLChild writes a map value after its key: a scalar on the same line,
// a map or list on the lines below.
func writeYAMLChild(w io.Writer, v any, indent int) {
	switch value := v.(type) {
	case map[string]any:
		if len(value) == 0 {
			fmt.Fprint(w, " {}\n")
			return
		}
		fmt.Fprint(w, "\n")
		writeYAMLValue(w, value, indent+1, false)
	case []any:
		if len(value) == 0 {
			fmt.Fprint(w, " []\n")
			return
		}
		fmt.Fprint(w, "\n")
		writeYAMLValue(w, value, indent+1, false)
	default:
		fmt.Fprint(w, " "+yamlScalar(value)+"\n")
	}
}

func yamlScalar(v any) string {
	switch value := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(value)
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case json.Number:
		return value.String()
	case string:
		return strconv.Quote(value)
	default:
		return strconv.Quote(fmt.Sprint(value))
	}
}
