package deploy

import (
	"path"
	"regexp"
	"strings"
)

// dockerignoreRule is one line of a .dockerignore, compiled with the
// semantics BuildKit applies (moby/patternmatcher): paths are relative to
// the context root, `**` crosses directories, a pattern that matches a
// directory excludes everything under it, `!` re-includes, and the last
// matching line wins.
type dockerignoreRule struct {
	Line    string
	Pattern string
	Negate  bool
	re      *regexp.Regexp
}

func parseDockerignore(content []byte) []dockerignoreRule {
	rules, _ := readDockerignore(content)
	return rules
}

// readDockerignore is parseDockerignore with the count of lines it could not
// read as a pattern, which BuildKit refuses and which are left out.
func readDockerignore(content []byte) ([]dockerignoreRule, int) {
	rules, unreadable := []dockerignoreRule{}, 0
	for _, raw := range strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rule := dockerignoreRule{Line: line}
		pattern := line
		if strings.HasPrefix(pattern, "!") {
			rule.Negate = true
			pattern = strings.TrimSpace(pattern[1:])
		}
		pattern = path.Clean(pattern)
		if len(pattern) > 1 && pattern[0] == '/' {
			pattern = pattern[1:]
		}
		if pattern == "" || pattern == "." {
			continue
		}
		expression, ok := dockerignoreRegexp(pattern)
		if !ok {
			unreadable++
			continue
		}
		rule.Pattern, rule.re = pattern, expression
		rules = append(rules, rule)
		if len(rules) >= 1024 {
			break
		}
	}
	return rules, unreadable
}

func dockerignoreRegexp(pattern string) (*regexp.Regexp, bool) {
	var expression strings.Builder
	expression.WriteString("^")
	for index := 0; index < len(pattern); index++ {
		char := pattern[index]
		switch char {
		case '*':
			if index+1 < len(pattern) && pattern[index+1] == '*' {
				index++
				// `**/` matches no directory at all as well as any number.
				if index+1 < len(pattern) && pattern[index+1] == '/' {
					index++
					expression.WriteString("(?:.*/)?")
				} else {
					expression.WriteString(".*")
				}
				continue
			}
			expression.WriteString("[^/]*")
		case '?':
			expression.WriteString("[^/]")
		case '[':
			end := strings.IndexByte(pattern[index:], ']')
			if end < 0 {
				return nil, false
			}
			class := pattern[index+1 : index+end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			expression.WriteString("[" + strings.ReplaceAll(class, `\`, `\\`) + "]")
			index += end
		case '\\':
			if index+1 < len(pattern) {
				index++
				expression.WriteString(regexp.QuoteMeta(string(pattern[index])))
			}
		default:
			expression.WriteString(regexp.QuoteMeta(string(char)))
		}
	}
	expression.WriteString("$")
	compiled, err := regexp.Compile(expression.String())
	return compiled, err == nil
}

// matches says whether the rule names the path or one of its parents.
func (r dockerignoreRule) matches(relative string) bool {
	if r.re.MatchString(relative) {
		return true
	}
	parts := strings.Split(relative, "/")
	for index := 1; index < len(parts); index++ {
		if r.re.MatchString(strings.Join(parts[:index], "/")) {
			return true
		}
	}
	return false
}

// dockerignoreExcludes reports whether the rules leave a context path out,
// and which line decided it.
func dockerignoreExcludes(rules []dockerignoreRule, relative string) (bool, string) {
	relative = strings.TrimPrefix(path.Clean("/"+relative), "/")
	if relative == "" {
		return false, ""
	}
	excluded, decidedBy := false, ""
	for _, rule := range rules {
		if rule.matches(relative) {
			excluded, decidedBy = !rule.Negate, rule.Line
		}
	}
	return excluded, decidedBy
}

// dockerignoreReincludesUnder says whether a later `!` rule brings back
// something beneath a directory an earlier rule excluded, which BuildKit
// honours by walking into it.
func dockerignoreReincludesUnder(rules []dockerignoreRule, directory string) bool {
	for _, rule := range rules {
		if rule.Negate && (strings.HasPrefix(rule.Pattern, directory+"/") || strings.HasPrefix(rule.Pattern, "**")) {
			return true
		}
	}
	return false
}
