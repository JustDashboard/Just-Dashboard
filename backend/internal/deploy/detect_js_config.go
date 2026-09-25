package deploy

import (
	"regexp"
	"strings"
)

// A JavaScript or TypeScript configuration file names the sub-path a site is
// built for as one key of the object it exports. The same key appears
// nested elsewhere — VitePress's sidebar groups carry their own `base`,
// Nuxt's runtimeConfig a `baseURL` for an API — and in comments, so a regular
// expression over the whole file served a site built for / under a sub-path
// it never had. The reader below finds the exported object and reads keys
// directly inside it, as text: comments are blanked, strings and brackets are
// tracked, and nothing is evaluated.

// jsConfigValue is what a configuration file gives a key: a literal string,
// or a value computed while the tool runs (a variable, a call, a template
// with a placeholder).
type jsConfigValue struct {
	literal    string
	found      bool
	expression bool
}

// jsConfig is a configuration file with its comments blanked, and the depth
// of every byte: -1 inside a string, else how many brackets enclose it.
type jsConfig struct {
	code  []byte
	depth []int
}

func readJSConfig(content []byte) jsConfig {
	config := jsConfig{code: jsBlankComments(content), depth: make([]int, len(content))}
	depth := 0
	for index := 0; index < len(config.code); index++ {
		switch c := config.code[index]; c {
		case '"', '\'', '`':
			config.depth[index] = depth
			end := jsStringEnd(config.code, index)
			for inner := index + 1; inner <= end && inner < len(config.code); inner++ {
				config.depth[inner] = -1
			}
			index = end
		case '{', '[', '(':
			config.depth[index] = depth
			depth++
		case '}', ']', ')':
			if depth > 0 {
				depth--
			}
			config.depth[index] = depth
		default:
			config.depth[index] = depth
		}
	}
	return config
}

// jsBlankComments replaces // and /* */ comments outside strings with
// spaces, keeping every other byte where it was.
func jsBlankComments(content []byte) []byte {
	code := append([]byte(nil), content...)
	for index := 0; index < len(code); index++ {
		switch {
		case code[index] == '"' || code[index] == '\'' || code[index] == '`':
			index = jsStringEnd(code, index)
		case code[index] == '/' && index+1 < len(code) && code[index+1] == '/':
			for ; index < len(code) && code[index] != '\n'; index++ {
				code[index] = ' '
			}
		case code[index] == '/' && index+1 < len(code) && code[index+1] == '*':
			end := len(code)
			if close := strings.Index(string(code[index+2:]), "*/"); close >= 0 {
				end = index + 2 + close + 2
			}
			for ; index < end; index++ {
				if code[index] != '\n' {
					code[index] = ' '
				}
			}
			index--
		}
	}
	return code
}

// jsStringEnd is the index of the quote that closes the string opening at
// start; a quote or apostrophe string also ends at its line.
func jsStringEnd(code []byte, start int) int {
	quote := code[start]
	for index := start + 1; index < len(code); index++ {
		switch code[index] {
		case '\\':
			index++
		case quote:
			return index
		case '\n':
			if quote != '`' {
				return index
			}
		}
	}
	return len(code) - 1
}

var (
	jsExportRE      = regexp.MustCompile(`\bexport\s+default\s+|\bmodule\.exports\s*=\s*`)
	jsIdentifierRE  = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*`)
	jsGenericArgsRE = regexp.MustCompile(`^\s*<[^<>()]*>`)
)

// value reads a key path of the exported object: ["base"], or
// ["app", "baseURL"] for a key of a nested object literal.
func (c jsConfig) value(keys ...string) jsConfigValue {
	for _, export := range jsExportRE.FindAllIndex(c.code, 4) {
		if c.depth[export[0]] != 0 {
			continue
		}
		object := c.objectAfter(export[1], 0)
		if object < 0 {
			continue
		}
		for index, key := range keys {
			at, found := c.key(object, key)
			if !found {
				break
			}
			if at < 0 {
				// `{ base }`: the value is a variable of the file.
				return jsConfigValue{found: true, expression: true}
			}
			if index < len(keys)-1 {
				if c.code[at] != '{' {
					return jsConfigValue{found: true, expression: true}
				}
				object = at
				continue
			}
			return c.literal(at)
		}
	}
	return jsConfigValue{}
}

// objectAfter is the object literal an expression starting at index is, or
// hands its tool: `{…}`, the first argument of a call (defineConfig({…}),
// withPlugin(config)), what an arrow function or a function returns, or the
// literal a variable was declared with.
func (c jsConfig) objectAfter(index, hops int) int {
	index = c.skipSpace(index)
	if hops > 4 || index >= len(c.code) {
		return -1
	}
	if strings.HasPrefix(string(c.code[index:]), "async ") {
		index = c.skipSpace(index + len("async "))
	}
	if index >= len(c.code) {
		return -1
	}
	switch c.code[index] {
	case '{':
		return index
	case '(':
		close := c.closing(index)
		if close < 0 {
			return -1
		}
		after := c.skipSpace(close + 1)
		if !strings.HasPrefix(string(c.code[after:]), "=>") {
			// A parenthesised expression: `export default ({…})`.
			return c.objectAfter(index+1, hops+1)
		}
		body := c.skipSpace(after + 2)
		switch {
		case body < len(c.code) && c.code[body] == '(':
			return c.objectAfter(body+1, hops+1)
		case body < len(c.code) && c.code[body] == '{':
			return c.returned(body)
		}
		return -1
	}
	name := jsIdentifierRE.Find(c.code[index:])
	if name == nil {
		return -1
	}
	after := index + len(name)
	if generic := jsGenericArgsRE.Find(c.code[after:]); generic != nil {
		after += len(generic)
	}
	after = c.skipSpace(after)
	switch {
	case string(name) == "function":
		open := strings.IndexByte(string(c.code[after:]), '(')
		if open < 0 {
			return -1
		}
		close := c.closing(after + open)
		if close < 0 {
			return -1
		}
		body := strings.IndexByte(string(c.code[close:]), '{')
		if body < 0 {
			return -1
		}
		return c.returned(close + body)
	case after < len(c.code) && c.code[after] == '(':
		return c.objectAfter(after+1, hops+1)
	}
	declaration := regexp.MustCompile(`\b(?:const|let|var)\s+` + regexp.QuoteMeta(string(name)) + `\b[^=;\n]*=\s*`).FindIndex(c.code)
	if declaration == nil || c.depth[declaration[0]] != 0 {
		return -1
	}
	return c.objectAfter(declaration[1], hops+1)
}

// returned is the object literal a function body at open returns.
func (c jsConfig) returned(open int) int {
	inside := c.depth[open] + 1
	for index := open + 1; index < len(c.code); index++ {
		if c.depth[index] < inside && c.code[index] == '}' {
			return -1
		}
		if c.depth[index] != inside || !c.word(index, "return") {
			continue
		}
		value := c.skipSpace(index + len("return"))
		if value < len(c.code) && c.code[value] == '(' {
			value = c.skipSpace(value + 1)
		}
		if value < len(c.code) && c.code[value] == '{' {
			return value
		}
		return -1
	}
	return -1
}

// key finds a key directly inside the object literal at open: the index of
// its value, or -1 with found for the shorthand `{ key }`.
func (c jsConfig) key(open int, key string) (int, bool) {
	inside := c.depth[open] + 1
	for index := open + 1; index < len(c.code); index++ {
		if c.depth[index] < inside && c.code[index] == '}' {
			return -1, false
		}
		if c.depth[index] != inside || !c.keyPosition(index, open) {
			continue
		}
		end := -1
		switch c.code[index] {
		case '"', '\'':
			close := jsStringEnd(c.code, index)
			if string(c.code[index+1:close]) == key {
				end = close + 1
			}
		default:
			if c.word(index, key) {
				end = index + len(key)
			}
		}
		if end < 0 {
			continue
		}
		next := c.skipSpace(end)
		switch {
		case next < len(c.code) && c.code[next] == ':':
			return c.skipSpace(next + 1), true
		case next < len(c.code) && (c.code[next] == ',' || c.code[next] == '}'):
			return -1, true
		}
	}
	return -1, false
}

// keyPosition says index starts a property: the first thing in the object,
// or the first after a comma, never the middle of a value.
func (c jsConfig) keyPosition(index, open int) bool {
	for previous := index - 1; previous >= open; previous-- {
		switch c.code[previous] {
		case ' ', '\t', '\n', '\r':
			continue
		case '{':
			return previous == open
		case ',':
			return true
		}
		return false
	}
	return false
}

// literal reads the value at index: a string with no placeholder is a
// literal; anything else is computed.
func (c jsConfig) literal(index int) jsConfigValue {
	quote := c.code[index]
	if quote != '"' && quote != '\'' && quote != '`' {
		return jsConfigValue{found: true, expression: true}
	}
	close := jsStringEnd(c.code, index)
	if close <= index || c.code[close] != quote {
		return jsConfigValue{found: true, expression: true}
	}
	text := string(c.code[index+1 : close])
	if strings.ContainsAny(text, "\\") || (quote == '`' && strings.Contains(text, "${")) {
		return jsConfigValue{found: true, expression: true}
	}
	return jsConfigValue{literal: text, found: true}
}

// word says a whole identifier starts at index.
func (c jsConfig) word(index int, word string) bool {
	if !strings.HasPrefix(string(c.code[index:min(len(c.code), index+len(word))]), word) {
		return false
	}
	identifier := func(b byte) bool {
		return b == '_' || b == '$' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
	}
	return (index == 0 || !identifier(c.code[index-1])) && (index+len(word) >= len(c.code) || !identifier(c.code[index+len(word)]))
}

// closing is the bracket that closes the one at open.
func (c jsConfig) closing(open int) int {
	for index := open + 1; index < len(c.code); index++ {
		if c.depth[index] == c.depth[open] && (c.code[index] == ')' || c.code[index] == '}' || c.code[index] == ']') {
			return index
		}
	}
	return -1
}

func (c jsConfig) skipSpace(index int) int {
	for index < len(c.code) && (c.code[index] == ' ' || c.code[index] == '\t' || c.code[index] == '\n' || c.code[index] == '\r') {
		index++
	}
	return index
}
