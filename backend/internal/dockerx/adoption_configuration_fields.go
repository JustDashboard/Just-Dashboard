package dockerx

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
)

func adoptionUnrepresentedConfiguration(raw []byte) ([]string, error) {
	var inspection map[string]any
	if err := json.Unmarshal(raw, &inspection); err != nil {
		return nil, errors.New("the original Docker configuration representation could not be checked")
	}
	var fields []string
	for _, section := range []struct {
		name string
		kind reflect.Type
	}{{"Config", reflect.TypeFor[container.Config]()}, {"HostConfig", reflect.TypeFor[container.HostConfig]()}} {
		collectAdoptionUnknownFields(inspection[section.name], section.kind, section.name, &fields)
	}
	sort.Strings(fields)
	return slices.Compact(fields), nil
}

func collectAdoptionUnknownFields(value any, kind reflect.Type, location string, unknown *[]string) {
	for kind.Kind() == reflect.Pointer {
		kind = kind.Elem()
	}
	switch kind.Kind() {
	case reflect.Struct:
		mapping, ok := value.(map[string]any)
		if !ok {
			return
		}
		known := adoptionJSONFields(kind)
		for name, raw := range mapping {
			field, represented := known[name]
			if !represented {
				if raw != nil {
					*unknown = append(*unknown, location+"."+name)
				}
				continue
			}
			collectAdoptionUnknownFields(raw, field, location+"."+name, unknown)
		}
	case reflect.Map:
		if mapping, ok := value.(map[string]any); ok {
			for _, raw := range mapping {
				// Dynamic labels/options are represented maps; their names are
				// configuration values rather than future Engine field names.
				collectAdoptionUnknownFields(raw, kind.Elem(), location+"[]", unknown)
			}
		}
	case reflect.Slice, reflect.Array:
		if entries, ok := value.([]any); ok {
			for _, raw := range entries {
				collectAdoptionUnknownFields(raw, kind.Elem(), location+"[]", unknown)
			}
		}
	}
}

func adoptionJSONFields(kind reflect.Type) map[string]reflect.Type {
	fields := map[string]reflect.Type{}
	for index := 0; index < kind.NumField(); index++ {
		field := kind.Field(index)
		if field.PkgPath != "" {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if field.Anonymous && name == "" {
			embedded := field.Type
			for embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				for name, kind := range adoptionJSONFields(embedded) {
					fields[name] = kind
				}
				continue
			}
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
	return fields
}
