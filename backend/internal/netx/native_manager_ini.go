package netx

import (
	"errors"
	"strings"
)

type nativeINIBlock struct {
	Section string
	Lines   []string
}
type nativeINI []nativeINIBlock

func parseNativeINI(data []byte) (nativeINI, error) {
	if len(data) > maxNativeProfileBytes || strings.ContainsRune(string(data), '\x00') {
		return nil, errors.New("invalid bounded native profile")
	}
	blocks := nativeINI{{}}
	// A terminal LF ends the last line; splitting it would invent a blank line.
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") {
			if !strings.HasSuffix(trim, "]") || strings.Count(trim, "[") != 1 {
				return nil, errors.New("malformed native profile section")
			}
			blocks = append(blocks, nativeINIBlock{Section: trim[1 : len(trim)-1]})
		} else {
			if trim != "" && !strings.HasPrefix(trim, "#") && !strings.HasPrefix(trim, ";") {
				if key, _, found := strings.Cut(trim, "="); !found || strings.TrimSpace(key) == "" || strings.HasSuffix(trim, "\\") {
					return nil, errors.New("unsupported native multiline or malformed property")
				}
			}
			blocks[len(blocks)-1].Lines = append(blocks[len(blocks)-1].Lines, line)
		}
	}
	return blocks, nil
}
func (blocks nativeINI) values(section, key string) []string {
	var result []string
	for _, block := range blocks {
		if block.Section == section {
			for _, line := range block.Lines {
				k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
				if ok && strings.TrimSpace(k) == key {
					result = append(result, strings.TrimSpace(v))
				}
			}
		}
	}
	return result
}
func (blocks nativeINI) one(section, key string) (string, error) {
	values := blocks.values(section, key)
	if len(values) > 1 {
		return "", errors.New("duplicate native profile property requires review through its owner")
	}
	if len(values) == 0 {
		return "", nil
	}
	return values[0], nil
}
func (blocks nativeINI) update(section string, remove func(string) bool, additions []string) nativeINI {
	result := nativeINI{}
	found := false
	for _, block := range blocks {
		if block.Section != section {
			result = append(result, block)
			continue
		}
		var lines []string
		for _, line := range block.Lines {
			key, _, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok || !remove(strings.TrimSpace(key)) {
				lines = append(lines, line)
			}
		}
		if !found {
			lines = append(lines, additions...)
			found = true
		}
		result = append(result, nativeINIBlock{Section: block.Section, Lines: lines})
	}
	if !found {
		result = append(result, nativeINIBlock{Section: section, Lines: additions})
	}
	return result
}
func (blocks nativeINI) render() []byte {
	var text strings.Builder
	for _, block := range blocks {
		if block.Section != "" {
			text.WriteString("[" + block.Section + "]\n")
		}
		for _, line := range block.Lines {
			text.WriteString(line + "\n")
		}
	}
	return []byte(text.String())
}
