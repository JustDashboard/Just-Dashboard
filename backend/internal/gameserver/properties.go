package gameserver

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
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
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "!") {
			file.Entries = append(file.Entries, PropertyEntry{Raw: line, Comment: true})
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			file.Entries = append(file.Entries, PropertyEntry{Raw: line, Comment: true})
			continue
		}
		// Raw keeps the line exactly as the server wrote it, including any
		// trailing whitespace a trimmed value would silently discard. It is
		// used verbatim until this key is actually changed.
		file.Entries = append(file.Entries, PropertyEntry{
			Key: strings.TrimSpace(key), Value: strings.TrimSpace(value), Raw: line,
		})
	}
	return file
}

func (f *PropertyFile) Get(key string) (string, bool) {
	for _, entry := range f.Entries {
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
	for index := range f.Entries {
		if !f.Entries[index].Comment && f.Entries[index].Key == key {
			f.Entries[index].Value = value
			f.Entries[index].Changed = true
			return nil
		}
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
			builder.WriteString(entry.Value)
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
		if property.Minimum != 0 && number < property.Minimum {
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
