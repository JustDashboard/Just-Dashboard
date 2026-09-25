package deploy

import (
	"strconv"
	"strings"
)

// pythonMarkerAllows evaluates a PEP 508 environment marker for a Linux
// CPython build of one family on the given Go architecture. What the marker
// asks that the build does not settle (an extra, an unknown variable, a
// marker it cannot parse) counts as holding, so a package is left out only
// when the marker rules the build out.
func pythonMarkerAllows(marker string, minor int, arch string) bool {
	tokens, ok := pythonMarkerTokens(marker)
	if !ok {
		return true
	}
	parser := pythonMarkerParser{
		tokens: tokens,
		environment: map[string]string{
			"python_version": "3." + strconv.Itoa(minor),
			// The catalogue images carry a family's latest patch release.
			"python_full_version":            "3." + strconv.Itoa(minor) + ".99",
			"sys_platform":                   "linux",
			"platform_system":                "Linux",
			"os_name":                        "posix",
			"implementation_name":            "cpython",
			"platform_python_implementation": "CPython",
		},
	}
	if machine := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[arch]; machine != "" {
		parser.environment["platform_machine"] = machine
	}
	value := parser.or()
	if parser.failed || parser.position != len(tokens) {
		return true
	}
	return value
}

type pythonMarkerToken struct {
	text   string
	quoted bool
}

func pythonMarkerTokens(marker string) ([]pythonMarkerToken, bool) {
	var tokens []pythonMarkerToken
	for index := 0; index < len(marker); {
		character := marker[index]
		switch {
		case character == ' ' || character == '\t':
			index++
		case character == '(' || character == ')':
			tokens = append(tokens, pythonMarkerToken{text: string(character)})
			index++
		case character == '\'' || character == '"':
			end := strings.IndexByte(marker[index+1:], character)
			if end < 0 {
				return nil, false
			}
			tokens = append(tokens, pythonMarkerToken{text: marker[index+1 : index+1+end], quoted: true})
			index += end + 2
		case strings.IndexByte("<>=!~", character) >= 0:
			end := index
			for end < len(marker) && strings.IndexByte("<>=!~", marker[end]) >= 0 {
				end++
			}
			tokens = append(tokens, pythonMarkerToken{text: marker[index:end]})
			index = end
		case character == '_' || character == '.' || character >= '0' && character <= '9' || character|0x20 >= 'a' && character|0x20 <= 'z':
			end := index
			for end < len(marker) && (marker[end] == '_' || marker[end] == '.' || marker[end] >= '0' && marker[end] <= '9' || marker[end]|0x20 >= 'a' && marker[end]|0x20 <= 'z') {
				end++
			}
			tokens = append(tokens, pythonMarkerToken{text: marker[index:end]})
			index = end
		default:
			return nil, false
		}
	}
	return tokens, len(tokens) > 0
}

type pythonMarkerParser struct {
	tokens      []pythonMarkerToken
	position    int
	environment map[string]string
	failed      bool
}

func (p *pythonMarkerParser) peek(text string) bool {
	return p.position < len(p.tokens) && !p.tokens[p.position].quoted && p.tokens[p.position].text == text
}

// or and and evaluate every operand, so a malformed tail is still noticed.
func (p *pythonMarkerParser) or() bool {
	value := p.and()
	for p.peek("or") {
		p.position++
		right := p.and()
		value = value || right
	}
	return value
}

func (p *pythonMarkerParser) and() bool {
	value := p.atom()
	for p.peek("and") {
		p.position++
		right := p.atom()
		value = value && right
	}
	return value
}

func (p *pythonMarkerParser) atom() bool {
	if p.peek("(") {
		p.position++
		value := p.or()
		if !p.peek(")") {
			p.failed = true
			return true
		}
		p.position++
		return value
	}
	if p.position+3 > len(p.tokens) {
		p.failed = true
		return true
	}
	left := p.tokens[p.position]
	p.position++
	operator := p.tokens[p.position].text
	p.position++
	if operator == "not" {
		if !p.peek("in") {
			p.failed = true
			return true
		}
		operator = "not in"
		p.position++
	}
	if p.position >= len(p.tokens) {
		p.failed = true
		return true
	}
	right := p.tokens[p.position]
	p.position++
	return p.compare(left, operator, right)
}

// compare settles one comparison; a variable the build does not know, or an
// ordering between names, holds.
func (p *pythonMarkerParser) compare(left pythonMarkerToken, operator string, right pythonMarkerToken) bool {
	reversed := map[string]string{"<": ">", "<=": ">=", ">": "<", ">=": "<="}
	variable, literal := left, right
	if left.quoted && !right.quoted {
		variable, literal = right, left
		switch operator {
		case "in":
			value, known := p.environment[variable.text]
			return !known || strings.Contains(value, literal.text)
		case "not in":
			value, known := p.environment[variable.text]
			return !known || !strings.Contains(value, literal.text)
		}
		if flipped, found := reversed[operator]; found {
			operator = flipped
		}
	}
	if variable.quoted || !literal.quoted {
		return true
	}
	value, known := p.environment[variable.text]
	if !known {
		return true
	}
	switch operator {
	case "in":
		return strings.Contains(literal.text, value)
	case "not in":
		return !strings.Contains(literal.text, value)
	}
	if variable.text != "python_version" && variable.text != "python_full_version" {
		switch operator {
		case "==", "===":
			return value == literal.text
		case "!=":
			return value != literal.text
		}
		return true
	}
	version := literal.text
	if strings.HasSuffix(version, ".*") && (operator == "==" || operator == "!=") {
		prefix := strings.TrimSuffix(version, ".*")
		matches := value == prefix || strings.HasPrefix(value, prefix+".")
		return matches == (operator == "==")
	}
	order := comparePythonVersions(value, version)
	switch operator {
	case "<":
		return order < 0
	case "<=":
		return order <= 0
	case ">":
		return order > 0
	case ">=":
		return order >= 0
	case "==", "===":
		return order == 0
	case "!=":
		return order != 0
	case "~=":
		parts := strings.Split(version, ".")
		if len(parts) < 2 {
			return true
		}
		return order >= 0 && comparePythonVersions(value, nextPythonRelease(strings.Join(parts[:len(parts)-1], "."), len(parts)-2)) < 0
	}
	return true
}
