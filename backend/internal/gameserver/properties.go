package gameserver

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

var ErrInvalidProperty = errors.New("server property is invalid")

// PropertyEntry is one line of a Java properties file, kept in the order it was
// read so a round trip preserves the operator's own file.
type PropertyEntry struct {
	Key   string
	Value string
	// Raw holds the line exactly as it was read. For a comment or blank line
	// it is the whole line; for a key it is the untouched original, so a file
	// nobody edited round-trips byte for byte.
	Raw     string
	Comment bool
	// Changed marks a key this editor rewrote. Only those lines are rendered
	// from Key and Value; everything else keeps its original bytes.
	Changed bool
}

type PropertyFile struct {
	Entries []PropertyEntry
}

// ParseProperties reads a Java properties file. It is deliberately forgiving
// about what it does not understand and exact about what it does: an unknown
// key survives a round trip byte-for-byte.
func ParseProperties(content string) *PropertyFile {
	file := &PropertyFile{Entries: []PropertyEntry{}}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for index := 0; index < len(lines); index++ {
		raw, logical := lines[index], lines[index]
		trimmed := strings.TrimLeft(logical, " \t\f")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "!") {
			file.Entries = append(file.Entries, PropertyEntry{Raw: raw, Comment: true})
			continue
		}
		for oddTrailingSlash(logical) && index+1 < len(lines) {
			index++
			raw += "\n" + lines[index]
			logical = logical[:len(logical)-1] + strings.TrimLeft(lines[index], " \t\f")
		}
		logical = strings.TrimLeft(logical, " \t\f")
		split := len(logical)
		escaped := false
		for i, r := range logical {
			if !escaped && strings.ContainsRune("=: \t\f", r) {
				split = i
				break
			}
			if r == '\\' && !escaped {
				escaped = true
			} else {
				escaped = false
			}
		}
		key, value := logical[:split], strings.TrimLeft(logical[split:], " \t\f")
		if strings.HasPrefix(value, "=") || strings.HasPrefix(value, ":") {
			value = value[1:]
		}
		value = strings.TrimLeft(value, " \t\f")
		file.Entries = append(file.Entries, PropertyEntry{Key: unescapeProperty(key), Value: unescapeProperty(value), Raw: raw})
	}
	return file
}

func oddTrailingSlash(value string) bool {
	count := 0
	for index := len(value) - 1; index >= 0 && value[index] == '\\'; index-- {
		count++
	}
	return count%2 != 0
}
func unescapeProperty(value string) string {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' || i+1 == len(value) {
			out.WriteByte(value[i])
			continue
		}
		i++
		switch value[i] {
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 't':
			out.WriteByte('\t')
		case 'f':
			out.WriteByte('\f')
		case 'u':
			if i+4 < len(value) {
				if r, err := strconv.ParseUint(value[i+1:i+5], 16, 16); err == nil {
					decoded := rune(r)
					if decoded >= 0xD800 && decoded <= 0xDBFF && i+10 < len(value) && value[i+5:i+7] == `\u` {
						if low, err := strconv.ParseUint(value[i+7:i+11], 16, 16); err == nil && low >= 0xDC00 && low <= 0xDFFF {
							decoded = utf16.DecodeRune(decoded, rune(low))
							i += 6
						}
					}
					out.WriteRune(decoded)
					i += 4
					continue
				}
			}
			out.WriteString("\\u")
		default:
			out.WriteByte(value[i])
		}
	}
	return out.String()
}
func escapeProperty(value string) string {
	value = strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\r", "\\r", "\t", "\\t", "\f", "\\f").Replace(value)
	if strings.HasPrefix(value, " ") {
		value = "\\" + value
	}
	return value
}

// PublicProperties returns only the blueprint's editable fields. Raw files can
// include RCON passwords and plugin credentials unrelated to those controls.
func PublicProperties(content string, allowed []string) (map[string]string, string) {
	parsed := ParseProperties(content)
	values := map[string]string{}
	var preview strings.Builder
	for _, key := range allowed {
		if value, found := parsed.Get(key); found {
			values[key] = value
			preview.WriteString(key + "=" + escapeProperty(value) + "\n")
		}
	}
	return values, preview.String()
}

func (f *PropertyFile) Get(key string) (string, bool) {
	for index := len(f.Entries) - 1; index >= 0; index-- {
		entry := f.Entries[index]
		if !entry.Comment && entry.Key == key {
			return entry.Value, true
		}
	}
	return "", false
}

// Values returns every declared key as a map, for rendering the structured
// editor beside the raw preview.
func (f *PropertyFile) Values() map[string]string {
	values := map[string]string{}
	for _, entry := range f.Entries {
		if !entry.Comment {
			values[entry.Key] = entry.Value
		}
	}
	return values
}

// Set writes one key in place, or appends it if the file does not have it.
// Every other byte of the file is left exactly as it was.
func (f *PropertyFile) Set(key, value string) error {
	if key == "" || strings.ContainsAny(key, "=\x00\r\n") {
		return fmt.Errorf("%w: %q is not a property name", ErrInvalidProperty, key)
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%w: the value for %q contains a line break", ErrInvalidProperty, key)
	}
	found := false
	for index := range f.Entries {
		if !f.Entries[index].Comment && f.Entries[index].Key == key {
			f.Entries[index].Value = value
			f.Entries[index].Changed = true
			found = true
		}
	}
	if found {
		return nil
	}
	f.Entries = append(f.Entries, PropertyEntry{Key: key, Value: value, Changed: true})
	return nil
}

func (f *PropertyFile) Render() string {
	var builder strings.Builder
	for index, entry := range f.Entries {
		if !entry.Changed && entry.Raw != "" {
			builder.WriteString(entry.Raw)
		} else if entry.Comment {
			builder.WriteString(entry.Raw)
		} else {
			builder.WriteString(entry.Key)
			builder.WriteString("=")
			builder.WriteString(escapeProperty(entry.Value))
		}
		if index < len(f.Entries)-1 {
			builder.WriteString("\n")
		}
	}
	return builder.String()
}

// KnownProperty is one key the dashboard offers a structured control for. It
// mirrors the blueprint's declaration; keys absent from it stay in the raw
// preview and are never rewritten by the structured editor.
type KnownProperty struct {
	Key     string
	Kind    string
	Minimum int
	Maximum int
	Choices []string
}

// ApplyProperties writes only declared keys, and only values their declaration
// allows. A request naming an undeclared key is refused rather than passed
// through, because "safe known-property editing" means exactly that.
func ApplyProperties(
	file *PropertyFile,
	known []KnownProperty,
	changes map[string]string,
) ([]string, error) {
	declared := map[string]KnownProperty{}
	for _, property := range known {
		declared[property.Key] = property
	}
	keys := make([]string, 0, len(changes))
	for key := range changes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	applied := []string{}
	for _, key := range keys {
		property, found := declared[key]
		if !found {
			return nil, fmt.Errorf("%w: %q is not a property this dashboard edits", ErrInvalidProperty, key)
		}
		value := strings.TrimSpace(changes[key])
		if err := validatePropertyValue(property, value); err != nil {
			return nil, err
		}
		if current, exists := file.Get(key); exists && current == value {
			continue
		}
		if err := file.Set(key, value); err != nil {
			return nil, err
		}
		applied = append(applied, key)
	}
	return applied, nil
}

func validatePropertyValue(property KnownProperty, value string) error {
	switch property.Kind {
	case "number":
		number, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("%w: %q must be a whole number", ErrInvalidProperty, property.Key)
		}
		if number < property.Minimum {
			return fmt.Errorf("%w: %q must be at least %d", ErrInvalidProperty, property.Key, property.Minimum)
		}
		if property.Maximum != 0 && number > property.Maximum {
			return fmt.Errorf("%w: %q must be at most %d", ErrInvalidProperty, property.Key, property.Maximum)
		}
	case "boolean":
		if value != "true" && value != "false" {
			return fmt.Errorf("%w: %q must be true or false", ErrInvalidProperty, property.Key)
		}
	case "choice":
		for _, choice := range property.Choices {
			if choice == value {
				return nil
			}
		}
		return fmt.Errorf("%w: %q is not an offered value for %q", ErrInvalidProperty, value, property.Key)
	case "text":
		if strings.ContainsAny(value, "\x00\r\n") || len(value) > 512 {
			return fmt.Errorf("%w: %q is not a single line of at most 512 characters", ErrInvalidProperty, property.Key)
		}
	default:
		return fmt.Errorf("%w: %q has no editable kind", ErrInvalidProperty, property.Key)
	}
	return nil
}
